package risefront

import (
	"errors"
	"net"
	"os"
	"path"
	"strings"
	"syscall"
)

// PrefixDialer uses github.com/Microsoft/go-winio.{DialPipe,ListenPipe} on windows and net.{Dial,Listen} on other platforms.
type PrefixDialer struct {
	Prefix           string
	WorkingDirectory string
}

var _ Dialer = PrefixDialer{}

func (pd PrefixDialer) Listen(name string) (net.Listener, error) {
	return listen(path.Join(pd.WorkingDirectory, pd.Prefix+name))
}

func (pd PrefixDialer) Dial(name string) (net.Conn, error) {
	var builder strings.Builder
	builder.WriteString(pd.Prefix)
	builder.WriteString(name)

	c, err := dial(path.Join(pd.WorkingDirectory, builder.String()))

	// attempt to remove the file if nobody is listening
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

func (cfg Config) NewPrefixDialer(prefix string) PrefixDialer {
	// convert name to uppercase with underline
	name := strings.ToUpper(strings.ReplaceAll(cfg.Name, "-", "_"))
	if name == "" {
		name = "RISEFRONT"
	}
	workingDirectory := os.Getenv(name + "_WORKING_DIR")

	if workingDirectory == "" {
		workingDirectory = getWorkingDir()
	}

	return PrefixDialer{
		Prefix:           prefix,
		WorkingDirectory: workingDirectory,
	}
}
