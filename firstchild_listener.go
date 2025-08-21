package risefront

import (
	"net"
	"sync"
)

type firstChildListener struct {
	addr      net.Addr
	ch        chan net.Conn
	closeOnce sync.Once
	wg        sync.WaitGroup
	closed    chan struct{}
}

func (sl *firstChildListener) Forward(conn net.Conn) {
	sl.wg.Add(1)
	
	// Check if listener is closed before sending
	select {
	case <-sl.closed:
		// Listener is closed, don't forward the connection
		sl.wg.Done()
		conn.Close()
		return
	default:
	}
	
	select {
	case sl.ch <- conn:
	case <-sl.closed:
		// Listener was closed while trying to send
		sl.wg.Done()
		conn.Close()
	default:
		go func() {
			select {
			case sl.ch <- conn:
			case <-sl.closed:
				// Listener was closed while waiting
				sl.wg.Done()
				conn.Close()
			}
		}()
	}
}

func (sl *firstChildListener) Accept() (net.Conn, error) {
	c, ok := <-sl.ch
	if !ok {
		return nil, net.ErrClosed
	}
	sl.wg.Done()
	return c, nil
}

func (sl *firstChildListener) Addr() net.Addr {
	return sl.addr
}

func (sl *firstChildListener) Close() error {
	sl.closeOnce.Do(func() {
		close(sl.closed)
		sl.wg.Wait()
		close(sl.ch)
	})
	return nil
}

var _ net.Listener = &firstChildListener{}
