// Package risefront enables gracefully upgrading the server behind a tcp connection with zero-downtime
// (without disturbing running transfers or dropping incoming requests).
package risefront

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Storing global configurations for use in Restart functions
var (
	globalConfig *Config
)

// Dialer is used for the child-parent communication.
type Dialer interface {
	Listen(name string) (net.Listener, error)
	Dial(name string) (net.Conn, error)
}

type Config struct {
	Addresses []string                   // Addresses to listen to.
	Run       func([]net.Listener) error // Handle the connections. All open connections should be properly closed before returning (srv.Shutdown for http.Server for instance).

	Name          string                                                    // Name of the socket file
	Dialer        Dialer                                                    // Dialer for child-parent communication. Let empty for default dialer (PrefixDialer{}).
	Network       string                                                    // "tcp" (default if empty), "tcp4", "tcp6", "unix" or "unixpacket"
	ErrorHandler  func(kind string, err error)                              // Where errors should be logged (print to stderr by default)
	RestartSignal os.Signal                                                 // Signal to trigger a restart
	NoRestart     bool                                                      // Disables all restarts
	LogHandler    func(loglevel LogLevel, kind string, args ...interface{}) // Where debug messages should be logged

	_ struct{} // to later add fields without break compatibility.

	args []string // Arguments passed to the child process
}

// New calls cfg.Run with opened listeners.
//
//   - if no other instance of risefront is found running, it will actually listen on the given addresses.
//   - if a parent instance of risefront is found running, it will ask this parent to forward all new connections.
//   - if migrating from overseer, will automatically detect and use overseer's file descriptors.
//
// The parent will live as long as the context lives.
// The child will live as long as the parent is alive and no other child has been started.
func New(ctx context.Context, cfg Config) error {
	// Save the global configuration for use in Restart
	globalConfig = &cfg

	// Save the original arguments to pass to the child process
	cfg.args = os.Args

	// First, try to get file descriptors from overseer
	if listeners, err := FromOverseerFDs(); err != nil {
		return err
	} else if len(listeners) > 0 {
		WatchOverseerSignal(cfg.RestartSignal, listeners)
		return cfg.Run(listeners)
	}

	if cfg.Dialer == nil {
		cfg.Dialer = cfg.NewPrefixDialer("")
	}
	if cfg.Network == "" {
		cfg.Network = "tcp"
	}
	if cfg.ErrorHandler == nil {
		cfg.ErrorHandler = func(kind string, err error) {
			log.Println(kind, err)
		}
	}
	if cfg.LogHandler == nil {
		cfg.LogHandler = func(loglevel LogLevel, kind string, args ...interface{}) {
			if loglevel >= ErrorLevel {
				cfg.ErrorHandler(kind, fmt.Errorf("%v", args))
				return
			}
			log.Println(kind, args)
		}
	}
	if cfg.RestartSignal == nil {
		cfg.RestartSignal = SIGUSR1
	}

	var builder strings.Builder
	if cfg.Name != "" {
		builder.WriteString(cfg.Name)
	} else {
		builder.WriteString("risefront")
	}
	builder.WriteString(".sock")

	c, err := cfg.Dialer.Dial(builder.String())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return cfg.runParent(ctx, builder.String())
	}

	return cfg.runChild(c)
}

// forwarder is used to forward an incoming net.Conn to another actual handler.
// Either local (in parent case) or via a Dialer (in child case).
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

func (cfg Config) runParent(ctx context.Context, socket string) error {
	lnChild, err := cfg.Dialer.Listen(socket)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = lnChild.Close()
	}()
	defer func(lnChild net.Listener) {
		_ = lnChild.Close()
	}(lnChild)

	var wgInternalListeners sync.WaitGroup
	defer wgInternalListeners.Wait()

	firstChildListeners := make([]net.Listener, 0, len(cfg.Addresses))

	// listen on all provided addresses and forward to channel
	forwarderChs := make([]chan *forwarder, 0, len(cfg.Addresses))
	listeners := make([]net.Listener, 0, len(cfg.Addresses))

	closeListeners := func() {
		for _, ln := range listeners {
			err := ln.Close()
			if err != nil && !errors.Is(err, net.ErrClosed) {
				cfg.LogHandler(ErrorLevel, "parent.sock.Close", err)
			}
		}
	}

	// Set up the restart signal processor
	if !cfg.NoRestart {
		restartCh := make(chan os.Signal, 1)
		signal.Notify(restartCh, cfg.RestartSignal)
		go func() {
			for range restartCh {
				cfg.LogHandler(InfoLevel, "parent", "restart signal received, starting new child process")
				err := cfg.createChild()
				if err != nil {
					cfg.LogHandler(ErrorLevel, "parent.createChild", err)
					continue
				}
			}
		}()
	}

	for _, a := range cfg.Addresses {
		ln, err := net.Listen(cfg.Network, a)
		if err != nil {
			closeListeners()
			return err
		}
		listeners = append(listeners, ln)

		// external listener
		connCh := make(chan net.Conn, 1)
		go cfg.runExternalListener(ln, connCh)

		// first child listener
		sl := firstChildListener{
			addr: ln.Addr(),
			ch:   make(chan net.Conn),
		}
		firstChildListeners = append(firstChildListeners, &sl)

		// internal listener
		forwarderCh := make(chan *forwarder)
		forwarderChs = append(forwarderChs, forwarderCh)

		wgInternalListeners.Add(1)
		go cfg.runInternalListener(connCh, forwarderCh, &sl, wgInternalListeners.Done)
	}

	defer closeListeners()

	// All set, run first child
	go func() {
		err := cfg.Run(firstChildListeners)
		if err != nil {
			cfg.LogHandler(ErrorLevel, "parent.Run", err)
		}
	}()

	// listen for new children
	for {
		rw, err := lnChild.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			// Check if the error is due to listener being closed (normal shutdown)
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				if opErr.Op == "accept" && strings.Contains(opErr.Err.Error(), "use of closed network connection") {
					// This is expected when shutting down, don't log as error
					cfg.LogHandler(DebugLevel, "parent.sock.Accept", "child listener closed during shutdown")
					return nil
				}
			}

			// Check for other network closed errors
			if errors.Is(err, net.ErrClosed) {
				cfg.LogHandler(DebugLevel, "parent.sock.Accept", "child listener closed")
				return nil
			}

			cfg.LogHandler(ErrorLevel, "parent.sock.Accept", err)
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return err
		}

		newForwarders, err := cfg.handleChildRequest(rw)
		if err != nil {
			cfg.LogHandler(WarnLevel, "child.Request", err)
			continue
		}

		for i, fw := range newForwarders {
			forwarderChs[i] <- fw
		}
	}
}

func (cfg Config) runExternalListener(ln net.Listener, ch chan net.Conn) {
	defer close(ch)
	for {
		rw, err := ln.Accept()

		if err != nil {
			// Check if the error is due to listener being closed (normal shutdown)
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				if opErr.Op == "accept" && strings.Contains(opErr.Err.Error(), "use of closed network connection") {
					return
				}
			}

			// Check for other network closed errors
			if errors.Is(err, net.ErrClosed) {
				cfg.LogHandler(DebugLevel, "parent."+ln.Addr().String()+".Accept", "listener closed")
				return
			}

			cfg.LogHandler(WarnLevel, "parent."+ln.Addr().String()+".Accept", err)
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return
		}
		ch <- rw
	}
}

func (cfg Config) runInternalListener(connCh chan net.Conn, forwarderCh chan *forwarder, sl *firstChildListener, done func()) {
	defer done()

	fw := &forwarder{
		handle: sl.Forward,
		close: func() {
			_ = sl.Close()
		},
	}
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
}

func (cfg Config) handleChildRequest(rw io.ReadWriteCloser) ([]*forwarder, error) {
	decoder := json.NewDecoder(rw)
	var req childRequest
	err := decoder.Decode(&req)
	if err != nil {
		_ = rw.Close()
		return nil, err
	}
	if len(req.Addresses) != len(cfg.Addresses) {
		msg := fmt.Sprintf("wrong number of addresses, got %d, expected %d", len(req.Addresses), len(cfg.Addresses))
		_, _ = rw.Write([]byte(msg))

		_ = rw.Close()
		return nil, errors.New(msg)
	}

	forwarders := make([]*forwarder, 0, len(cfg.Addresses))
	var wgClose sync.WaitGroup
	for _, a := range req.Addresses {
		wgClose.Add(1)
		addr := a
		forwarders = append(forwarders, &forwarder{
			handle: func(cliConn net.Conn) {
				wgClose.Add(1)
				go func() {
					srvConn, err := cfg.Dialer.Dial(addr)
					wgClose.Done()
					if err != nil {
						_ = cliConn.Close()
						cfg.LogHandler(ErrorLevel, "child."+addr+".Dial", err)
						return
					}
					proxy(srvConn, cliConn)
				}()
			},
			close: wgClose.Done,
		})
	}

	// keepalive
	go func() {
		t := time.NewTicker(5 * time.Second)
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
		_ = rw.Close()
	}()

	fmt.Println(req.Addresses)
	return forwarders, nil
}

func (cfg Config) runChild(rw io.ReadWriteCloser) error {
	defer func(rw io.ReadWriteCloser) {
		_ = rw.Close()
	}(rw)

	listeners := make([]net.Listener, 0, len(cfg.Addresses))
	addresses := make([]string, 0, len(cfg.Addresses))
	prefix, err := randomPrefix(6)
	if err != nil {
		return err
	}

	closeListeners := func() {
		for _, ln := range listeners {
			err := ln.Close()
			if err != nil && !errors.Is(err, net.ErrClosed) {
				cfg.LogHandler(WarnLevel, "child.sock.Close", err)
			}
		}
	}

	var builder strings.Builder
	if cfg.Name != "" {
		builder.WriteString(cfg.Name)
	} else {
		builder.WriteString("rise")
	}
	builder.WriteString("-child-")
	builder.WriteString(prefix)
	builder.WriteString("-")

	for i := range cfg.Addresses {
		name := builder.String() + strconv.Itoa(i) + ".sock"

		// hack to ensure that a dangling socket gets cleaned up before listening
		if conn, _ := cfg.Dialer.Dial(name); conn != nil {
			_ = conn.Close()
		}

		ln, err := cfg.Dialer.Listen(name)
		if err != nil {
			closeListeners()
			return err
		}

		listeners = append(listeners, ln)
		addresses = append(addresses, name)
	}
	defer closeListeners()

	err = json.NewEncoder(rw).Encode(childRequest{
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
		closeListeners()
	}()

	if err := cfg.Run(listeners); !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func randomPrefix(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type childRequest struct {
	Addresses []string `json:"addresses"`
}

// Restart creates a child process instead of sending a signal
func Restart() {
	if globalConfig == nil {
		panic("globalConfig is nil, Restart() should only be called from within a child process")
	}

	err := globalConfig.createChild()
	if err != nil {
		globalConfig.LogHandler(ErrorLevel, "restart.createChild", err)
	}
}

// createChild creates a child process
func (cfg Config) createChild() error {
	// Create the child process
	cmd := exec.Command(cfg.args[0], cfg.args[1:]...)

	// Inherit the current process's environment variables
	cmd.Env = os.Environ()

	// Set the working directory to the current process's working directory
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	cmd.Dir = wd

	// Pass the listeners' file descriptors to the child process
	if isFromOverseer() {
		listeners, err := FromOverseerFDs()
		if err != nil {
			return err
		}

		// Get the file descriptor for each listener
		cmd.ExtraFiles = make([]*os.File, 0, len(listeners))
		for _, ln := range listeners {
			if l, ok := ln.(*net.TCPListener); ok {
				file, err := l.File()
				if err != nil {
					return err
				}
				cmd.ExtraFiles = append(cmd.ExtraFiles, file)
			}
		}
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Start the subprocess
	if err = cmd.Start(); err != nil {
		return err
	}

	cfg.LogHandler(DebugLevel, "parent", fmt.Errorf("child process started, PID: %d, %d listeners passed", cmd.Process.Pid, len(cmd.ExtraFiles)))
	return nil
}
