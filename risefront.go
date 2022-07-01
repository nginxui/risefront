package risefront

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
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

	fmt.Printf("%#v", err)
	fmt.Println(c, err, errors.Is(err, fs.ErrNotExist))

	// if os.Getenv(envIsChild) == "1" {
	// 	// TODO: create listeners
	// 	// communicate listeners to parent
	// 	// run
	// }
	//if parent

	// if child

	return nil
}

func (cfg Config) handleErr(kind string, err error) {
	fmt.Println(kind, err)
	if cfg.ErrorHandler != nil {
		cfg.ErrorHandler(kind, err)
	}
}

type forwarder func(net.Conn, error)

type wgCloser struct {
	net.Conn
	done func()
}

func (wc wgCloser) Close() error {
	err := wc.Conn.Close()
	fmt.Println("CLOSE", err)
	if wc.done != nil && (err == nil || !errors.Is(err, net.ErrClosed)) {
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
	childForwarders := make([]forwarder, 0, len(cfg.Addresses))

	var lock sync.RWMutex

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
			ch:   make(chan firstChildArgs),
			close: func() error {
				// wgChildrenListeners.Done()
				return nil
			},
		}
		firstChildListeners = append(firstChildListeners, sl)
		i := len(childForwarders)
		childForwarders = append(childForwarders, sl.Forward)

		wgChildrenListeners.Add(1) // for the listener
		go func() {
			for {
				wgChildrenListeners.Add(1) // for the connection
				rw, err := ln.Accept()
				fmt.Println("Accept", err)

				done := wgChildrenListeners.Done
				if err != nil {
					done()
					done = nil
				}

				lock.RLock()
				forward := childForwarders[i]
				lock.RUnlock()

				forward(wgCloser{
					Conn: rw,
					done: done,
				}, err)
				if err != nil && errors.Is(err, net.ErrClosed) {
					// no point continuing
					wgChildrenListeners.Done()
					return
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
			fmt.Println("child-error", err)
			fmt.Println(ctx.Err())
			if ctx.Err() != nil {
				return nil
			}
			cfg.handleErr("parent.sock.Accept", err)
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return err
		}
		go func() {
			defer rw.Close()
			// TODO
			fmt.Println("TODO: handle child", rw)

			// https://gist.github.com/jbardin/821d08cb64c01c84b81a
		}()
	}
}
