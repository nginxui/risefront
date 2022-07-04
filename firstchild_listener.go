package risefront

import (
	"fmt"
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
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func (sl *firstChildListener) Forward(conn net.Conn, err error) {
	sl.wg.Add(1)
	fmt.Println("fcl.Forward", err)
	select {
	case sl.ch <- firstChildArgs{
		conn: conn,
		err:  err,
	}:
	default:
		go func() {
			sl.ch <- firstChildArgs{
				conn: conn,
				err:  err,
			}
		}()
	}
}

func (sl *firstChildListener) Accept() (net.Conn, error) {
	a, ok := <-sl.ch
	fmt.Println("fcl.Accept", ok)
	if !ok {
		return nil, net.ErrClosed
	}
	sl.wg.Done()
	return a.conn, a.err
}

func (sl *firstChildListener) Addr() net.Addr {
	return sl.addr
}

func (sl *firstChildListener) Close() error {
	fmt.Println("fcl.Close")
	sl.wg.Wait()
	fmt.Println("fcl.Waited")
	sl.closeOnce.Do(func() {
		close(sl.ch)
	})
	return nil
}

var _ net.Listener = &firstChildListener{}
