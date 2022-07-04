package risefront

import (
	"net"
	"sync"
)

type firstChildArgs struct {
	conn net.Conn
	err  error
}
type firstChildListener struct {
	addr      net.Addr
	ch        chan firstChildArgs
	closeOnce *sync.Once
}

func (sl firstChildListener) Forward(conn net.Conn, err error) {
	sl.ch <- firstChildArgs{
		conn: conn,
		err:  err,
	}
}

func (sl firstChildListener) Accept() (net.Conn, error) {
	a, ok := <-sl.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return a.conn, a.err
}

func (sl firstChildListener) Addr() net.Addr {
	return sl.addr
}
func (sl firstChildListener) Close() error {
	sl.closeOnce.Do(func() {
		close(sl.ch)
	})
	return nil
}

var _ net.Listener = firstChildListener{}
