package risefront

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"
)

type Dialer interface {
	Listen(name string) (net.Listener, error)
	Dial(name string) (net.Conn, error)
}

type Config struct {
	Dialer    Dialer // let empty for default dialer
	Network   string // "tcp" (default), "tcp4", "tcp6", "unix" or "unixpacket"
	Addresses []string

	Run            func([]net.Listener) error // all running connections should be closed before returning (srv.Shutdown for instance)
	TriggerUpgrade chan<- struct{}
	ErrorHandler   func(string, error)
}

func New(ctx context.Context, cfg Config) error {
	if cfg.Dialer == nil {
		cfg.Dialer = PrefixDialer{}
	}
	if cfg.Network == "" {
		cfg.Network = "tcp"
	}
	c, err := cfg.Dialer.Dial("risefront.sock")
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return cfg.runParent(ctx)
	}

	return cfg.runChild(ctx, c)
}

func (cfg Config) handleErr(kind string, err error) {
	fmt.Println(kind, err)
	if cfg.ErrorHandler != nil {
		cfg.ErrorHandler(kind, err)
	}
}

type forwarder func(net.Conn)

type wgCloser struct {
	net.Conn
	done func()
}

func (wc wgCloser) Close() error {
	err := wc.Conn.Close()
	if err == nil || !errors.Is(err, net.ErrClosed) {
		wc.done()
	}
	return err
}

func (cfg Config) runParent(ctx context.Context) error {
	lnChild, err := cfg.Dialer.Listen("risefront.sock")
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		lnChild.Close()
	}()
	defer lnChild.Close()

	var wgChildrenListeners sync.WaitGroup
	defer wgChildrenListeners.Wait()

	firstChildListeners := make([]net.Listener, 0, len(cfg.Addresses))

	var lock sync.RWMutex
	childClosing := false
	childForwarders := make([]forwarder, 0, len(cfg.Addresses))
	childClose := func() error {
		for _, sl := range firstChildListeners {
			sl.Close()
		}
		return nil
	}

	defer func() {
		lock.Lock()
		childClosing = true
		childClose()
		lock.Unlock()
	}()

	// listen on all provided addresses
	// and populate the firstChildListeners
	for _, a := range cfg.Addresses {
		ln, err := net.Listen(cfg.Network, a)
		if err != nil {
			return err
		}
		defer ln.Close()

		sl := firstChildListener{
			addr: ln.Addr(),
			ch:   make(chan net.Conn),
		}
		firstChildListeners = append(firstChildListeners, &sl)
		i := len(childForwarders)
		childForwarders = append(childForwarders, sl.Forward)

		wgChildrenListeners.Add(1) // for the listener
		go func() {
			defer wgChildrenListeners.Done() // for the listener

			for {
				wgChildrenListeners.Add(1) // per connection

				rw, err := ln.Accept()

				// no point continuing if the err is net.ErrClosed
				if err != nil && errors.Is(err, net.ErrClosed) {
					wgChildrenListeners.Done() // per connection
					return
				}
				if err != nil {
					cfg.handleErr("Accept", err)
				}

				lock.RLock()
				isClosing := childClosing
				forward := childForwarders[i]
				lock.RUnlock()

				if !isClosing {
					go forward(wgCloser{
						Conn: rw,
						done: wgChildrenListeners.Done,
					})
				}
			}
		}()
	}

	// All set, run first child
	go cfg.Run(firstChildListeners)

	// listen for new children
	for {
		rw, err := lnChild.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			cfg.handleErr("parent.sock.Accept", err)
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return err
		}

		newForwarders, newChildClose, err := cfg.handleChildRequest(rw)
		if err != nil {
			cfg.handleErr("child.Request", err)
			continue
		}

		lock.Lock()
		previousCloser := childClose
		childForwarders = newForwarders
		childClose = newChildClose
		lock.Unlock()

		go previousCloser()
	}
}

func (cfg Config) handleChildRequest(rw io.ReadWriteCloser) ([]forwarder, func() error, error) {
	decoder := json.NewDecoder(rw)
	var req ChildRequest
	err := decoder.Decode(&req)
	if err != nil {
		rw.Close()
		return nil, nil, err
	}
	if len(req.Addresses) != len(cfg.Addresses) {
		msg := fmt.Sprintf("wrong number of addresses, got %d, expected %d", len(req.Addresses), len(cfg.Addresses))
		rw.Write([]byte(msg))

		rw.Close()
		return nil, nil, errors.New(msg)
	}

	forwarders := make([]forwarder, 0, len(cfg.Addresses))
	for _, a := range req.Addresses {
		addr := a
		forwarders = append(forwarders, func(cliConn net.Conn) {
			srvConn, err := cfg.Dialer.Dial(addr)
			if err != nil {
				cliConn.Close()
				cfg.handleErr(addr, err)
				return
			}

			go proxy(srvConn, cliConn)
		},
		)
	}

	// keepalive
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			_, err := rw.Write([]byte{0})
			if err != nil {
				return
			}
		}
	}()

	return forwarders, rw.Close, nil
}

func (cfg Config) runChild(ctx context.Context, rw io.ReadWriteCloser) error {
	defer rw.Close()

	listeners := make([]net.Listener, 0, len(cfg.Addresses))
	addresses := make([]string, 0, len(cfg.Addresses))
	prefix := "child-" + randStringBytesMaskImprSrc(6) + "-"
	for i := range cfg.Addresses {
		name := prefix + strconv.Itoa(i)

		// hack to ensure that a dangling socket gets cleaned up before listening
		if conn, _ := cfg.Dialer.Dial(name); conn != nil {
			conn.Close()
		}

		ln, err := cfg.Dialer.Listen(name)
		if err != nil {
			return err
		}
		defer ln.Close()

		listeners = append(listeners, ln)
		addresses = append(addresses, name)
	}

	encoder := json.NewEncoder(rw)
	err := encoder.Encode(ChildRequest{
		Addresses: addresses,
	})
	if err != nil {
		return err
	}

	go func() {
		b := make([]byte, 1)
		for {
			_, err := rw.Read(b)
			if err != nil {
				break
			}
		}
		for _, ln := range listeners {
			ln.Close()
		}
	}()

	return cfg.Run(listeners)
}

// https://stackoverflow.com/a/46909816/3207406
// RandStringBytesMaskImprSrc returns a random hexadecimal string of length n.
func randStringBytesMaskImprSrc(n int) string {
	b := make([]byte, (n+1)/2) // can be simplified to n/2 if n is always even
	rand.Read(b)               // always returns len(p) and a nil error
	return hex.EncodeToString(b)[:n]
}

type ChildRequest struct {
	Addresses []string `json:"addresses"`
}
