//go:build !windows

package risefront

import (
	"net"
	"os"
	"path"
)

var socketDirectory = os.TempDir()

func listen(name string) (net.Listener, error) {
	return net.Listen("unix", path.Join(socketDirectory, name))
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", path.Join(socketDirectory, name))
}
