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

	// Try to remove the socket file if it exists
	err := os.Remove(fullPath)
	if err != nil && !os.IsNotExist(err) {
		// If the error is not "file does not exist", this may indicate permission issues or other problems
		log.Printf("Warning: failed to remove existing socket file %s: %v", fullPath, err)

		// Check if the file is actually a socket, if not or cannot be accessed, try to check parent directory
		if stat, err := os.Stat(fullPath); err != nil || stat.Mode()&os.ModeSocket == 0 {
			// Create the parent directory (if it doesn't exist)
			dir := path.Dir(fullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("Warning: failed to create directory %s: %v", dir, err)
			}
		}
	}

	// Attempt to create the socket
	return net.Listen("unix", fullPath)
}

func dial(name string) (net.Conn, error) {
	return net.Dial("unix", path.Join(workingDirectory, name))
}
