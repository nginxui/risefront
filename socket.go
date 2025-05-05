//go:build !windows

package risefront

import (
	"log"
	"net"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

var socketDirectory = os.TempDir()

func listen(name string) (net.Listener, error) {
	return net.Listen("unix", path.Join(socketDirectory, name))
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", path.Join(socketDirectory, name))
}
