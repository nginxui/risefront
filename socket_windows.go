//go:build windows

package risefront

import (
	"net"

	"github.com/Microsoft/go-winio"
)

func listen(name string) (net.Listener, error) {
	return winio.ListenPipe(`\\.\pipe\`+name, nil)
}

func dial(name string) (net.Conn, error) {
	return winio.DialPipe(`\\.\pipe\`+name, nil)
}

func getWorkingDir() string {
	// Windows does not need it for named pipes
	return ""
}
