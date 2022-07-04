package risefront

import (
	"net"
	"os"
	"syscall"
)

// PrefixDialer uses github.com/Microsoft/go-winio.{DialPipe,ListenPipe} on windows and net.{Dial,Listen} on other platforms
type PrefixDialer struct {
	Prefix string
}

var _ Dialer = PrefixDialer{}

func (pd PrefixDialer) Listen(name string) (net.Listener, error) {
	return listen(pd.Prefix + name)
}

func (pd PrefixDialer) Dial(name string) (net.Conn, error) {
	c, err := dial(pd.Prefix + name)

	// attempt to remove file if nobofy is listening
	if err != nil && errorIsNobodyListening(err) {
		_ = os.Remove(pd.Prefix + name)
		// re-dial to have a nice fs.ErrNotExist error
		c, err = dial(pd.Prefix + name)
	}

	return c, err
}

func errorIsNobodyListening(err error) bool {
	netErr, ok := err.(*net.OpError)
	if !ok {
		return false
	}
	sysErr, ok := netErr.Err.(*os.SyscallError)
	if !ok {
		return false
	}
	if sysErr.Syscall != "connect" || sysErr.Err != syscall.Errno(111) {
		return false
	}
	return true
}
