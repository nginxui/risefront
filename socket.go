//go:build !windows

package risefront

import (
	"net"
)

func listen(name string) (net.Listener, error) {
	return net.Listen("unix", name)
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", name)
}
