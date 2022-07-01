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
	Dialer         Dialer // let empty for default dialer
	Network        string // "tcp" (default), "tcp4", "tcp6", "unix" or "unixpacket"
	Addresses      []string
	Run            func([]net.Listener) error
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

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, a := range cfg.Addresses {
		ln, err := net.Listen(cfg.Network, a)
		if err != nil {
			return err
		}
		defer ln.Close()

		go func() {
			for {
				rw, err := ln.Accept()
				if err != nil {
					cfg.handleErr("parent.listen."+ln.Addr().String()+".Accept", err)
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						time.Sleep(5 * time.Millisecond)
						continue
					}
					return
				}

				wg.Add(1)

				go func() {
					defer rw.Close()

					// TODO
					fmt.Println("TODO: handle incoming connection", ln.Addr().String(), rw)
					wg.Done()
				}()
			}
		}()
	}

	for {
		rw, err := lnChild.Accept()
		if err != nil {
			fmt.Println("child", err)
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
		}()
	}
}
