//go:build !windows

package risefront

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func listen(name string) (net.Listener, error) {
	return net.Listen("unix", name)
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", name)
}

func getWorkingDir() string {
	dir, _ := os.Getwd()
	if unix.Access(dir, unix.W_OK) != nil {
		return os.TempDir()
	}
	return dir
}
