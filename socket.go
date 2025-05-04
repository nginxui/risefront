//go:build !windows

package risefront

import (
	"log"
	"net"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

var workingDirectory string

func init() {
	workingDirectory, _ = os.Getwd()
	if unix.Access(workingDirectory, unix.W_OK) != nil {
		log.Println("The current working directory is not writable, using temp dir")
		workingDirectory = os.TempDir()
	}
}

func listen(name string) (net.Listener, error) {
	fullPath := path.Join(workingDirectory, name)
	// Try to remove the socket file if it exists (it might be a stale one from a previous run)
	_ = os.Remove(fullPath)
	return net.Listen("unix", fullPath)
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", path.Join(workingDirectory, name))
}
