package risefront

import (
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
)

// PrefixDialer uses github.com/Microsoft/go-winio.{DialPipe,ListenPipe} on windows and net.{Dial,Listen} on other platforms.
type PrefixDialer struct {
	Prefix string
}

var _ Dialer = PrefixDialer{}

func (pd PrefixDialer) Listen(name string) (net.Listener, error) {
	return listen(pd.Prefix + name)
}

func (pd PrefixDialer) Dial(name string) (net.Conn, error) {
	var builder strings.Builder
	builder.WriteString(pd.Prefix)
	builder.WriteString(name)

	c, err := dial(builder.String())

	// attempt to remove file if nobofy is listening
	if err != nil && errorIsNobodyListening(err) {
		_ = os.Remove(builder.String())
		// re-dial to have a nice fs.ErrNotExist error
		c, err = dial(builder.String())
	}

	return c, err
}

func errorIsNobodyListening(err error) bool {
	sysErr := &os.SyscallError{}
	if errors.As(err, &sysErr) {
		return sysErr.Syscall == "connect" && errors.Is(sysErr.Err, syscall.Errno(111))
	}
	return false
}
