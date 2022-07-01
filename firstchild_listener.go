package risefront

import "net"

type firstChildArgs struct {
	conn net.Conn
	err  error
}
type firstChildListener struct {
	addr  net.Addr
	ch    chan firstChildArgs
	close func() error
}

func (sl firstChildListener) Forward(conn net.Conn, err error) {
	sl.ch <- firstChildArgs{
		conn: conn,
		err:  err,
	}
}

func (sl firstChildListener) Accept() (net.Conn, error) {
	a := <-sl.ch
	return a.conn, a.err
}

func (sl firstChildListener) Addr() net.Addr {
	return sl.addr
}
func (sl firstChildListener) Close() error {
	if sl.close != nil {
		return sl.close()
	}
	return nil
}

var _ net.Listener = firstChildListener{}
