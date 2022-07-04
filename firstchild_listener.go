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
}

func (sl *firstChildListener) Forward(conn net.Conn) {
	sl.wg.Add(1)
	select {
	case sl.ch <- conn:
	default:
		go func() {
			sl.ch <- conn
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
	sl.wg.Wait()
	sl.closeOnce.Do(func() {
		close(sl.ch)
	})
	return nil
}

var _ net.Listener = &firstChildListener{}
