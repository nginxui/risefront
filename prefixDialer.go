package risefront

import "net"

// PrefixDialer uses github.com/Microsoft/go-winio.{DialPipe,ListenPipe} on windows and net.{Dial,Listen} on other platforms
type PrefixDialer struct {
	Prefix string
}

var _ Dialer = PrefixDialer{}

func (pd PrefixDialer) Listen(name string) (net.Listener, error) {
	return listen(pd.Prefix + name)
}

func (pd PrefixDialer) Dial(name string) (net.Conn, error) {
	return dial(pd.Prefix + name)
}
