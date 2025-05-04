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
	return net.Listen("unix", path.Join(workingDirectory, name))
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", path.Join(workingDirectory, name))
}
