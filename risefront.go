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

type forwarder struct {
	handle func(net.Conn)
	close  func()
	wg     sync.WaitGroup
}

func (f *forwarder) closeAndWait() {
	f.close()
	f.wg.Wait()
}

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

	var wgInternalListeners sync.WaitGroup
	defer wgInternalListeners.Wait()

	firstChildListeners := make([]net.Listener, 0, len(cfg.Addresses))

	// listen on all provided addresses and forward to channel
	forwarderChs := make([]chan *forwarder, 0, len(cfg.Addresses))
	for _, a := range cfg.Addresses {
		ln, err := net.Listen(cfg.Network, a)
		if err != nil {
			return err
		}
		defer ln.Close()

		// external listener
		connCh := make(chan net.Conn, 1)
		go func() {
			defer close(connCh)
			for {
				rw, err := ln.Accept()

				if err != nil {
					cfg.handleErr("parent.external.Accept", err)
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						time.Sleep(5 * time.Millisecond)
						continue
					}
					return
				}
				connCh <- rw
			}
		}()

		// first child listener
		sl := firstChildListener{
			addr: ln.Addr(),
			ch:   make(chan net.Conn),
		}
		firstChildListeners = append(firstChildListeners, &sl)
		fw := &forwarder{
			handle: sl.Forward,
			close: func() {
				sl.Close()
			},
		}

		// internal listener
		forwarderCh := make(chan *forwarder)
		forwarderChs = append(forwarderChs, forwarderCh)

		wgInternalListeners.Add(1)
		go func() {
			defer wgInternalListeners.Done()
			for {
				select {
				case conn, ok := <-connCh:
					if !ok { // channel closed
						fw.closeAndWait()
						return
					}
					fw.wg.Add(1)
					fw.handle(wgCloser{
						Conn: conn,
						done: fw.wg.Done,
					})

				case newFw := <-forwarderCh:
					oldFw := fw
					fw = newFw

					fw.wg.Add(1)
					go func() {
						oldFw.closeAndWait()
						fw.wg.Done()
					}()
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

		newForwarders, err := cfg.handleChildRequest(rw)
		if err != nil {
			cfg.handleErr("child.Request", err)
			continue
		}

		for i, fw := range newForwarders {
			forwarderChs[i] <- fw
		}
	}
}

func (cfg Config) handleChildRequest(rw io.ReadWriteCloser) ([]*forwarder, error) {
	decoder := json.NewDecoder(rw)
	var req ChildRequest
	err := decoder.Decode(&req)
	if err != nil {
		rw.Close()
		return nil, err
	}
	if len(req.Addresses) != len(cfg.Addresses) {
		msg := fmt.Sprintf("wrong number of addresses, got %d, expected %d", len(req.Addresses), len(cfg.Addresses))
		rw.Write([]byte(msg))

		rw.Close()
		return nil, errors.New(msg)
	}

	forwarders := make([]*forwarder, 0, len(cfg.Addresses))
	var wgClose sync.WaitGroup
	for _, a := range req.Addresses {
		wgClose.Add(1)
		addr := a
		forwarders = append(forwarders, &forwarder{
			handle: func(cliConn net.Conn) {
				srvConn, err := cfg.Dialer.Dial(addr)
				if err != nil {
					cliConn.Close()
					cfg.handleErr(addr, err)
					return
				}

				go proxy(srvConn, cliConn)
			},
			close: wgClose.Done,
		})
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

	// close connection when all forwarders are done
	go func() {
		wgClose.Wait()
		rw.Close()
	}()

	return forwarders, nil
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
