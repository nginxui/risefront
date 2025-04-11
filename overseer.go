// Package risefront provides utilities for migration from overseer
package risefront

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
)

// In applications using overseer, you can smoothly migrate to risefront as follows:
//
// 1. In the old version of the application using overseer, trigger a restart:
//    overseer.Restart()
//
// 2. In the new version, simply use risefront to automatically take over:
//
//    func main() {
//        // Create an HTTP handler
//        handler := http.NewServeMux()
//        handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
//            fmt.Fprintf(w, "Hello, World!")
//        })
//
//        // Start the service using risefront, which will automatically detect and take over overseer's file descriptors
//        err := risefront.New(context.Background(), risefront.Config{
//            Addresses: []string{":8080"},
//            Run: func(listeners []net.Listener) error {
//                server := &http.Server{
//                    Handler: handler,
//                }
//
//                // If multiple listeners exist, multiple servers can be started here
//                return server.Serve(listeners[0])
//            },
//        })
//
//        if err != nil {
//            log.Fatal(err)
//        }
//    }
//
// risefront will automatically:
// 1. Detect overseer's file descriptors from environment variables
// 2. If found, take over these descriptors and convert them into listeners
// 3. Listen for the original signals to support restart
// 4. Run the service using these listeners

// FromOverseerFDs retrieves file descriptors from overseer and creates listeners.
// If overseer file descriptors exist in the environment variables, corresponding listeners are returned.
// If not, it returns nil and nil.
func FromOverseerFDs() ([]net.Listener, error) {
	numFDs, err := strconv.Atoi(os.Getenv("OVERSEER_NUM_FDS"))
	if err != nil {
		return nil, nil
	}

	listeners := make([]net.Listener, 0, numFDs)
	for i := 0; i < numFDs; i++ {
		// Create a file object
		f := os.NewFile(uintptr(3+i), "")
		if f == nil {
			log.Printf("Failed to create file object #%d", i)
			continue
		}

		l, err := net.FileListener(f)
		if err != nil {
			return nil, fmt.Errorf("failed to take over overseer file descriptor: %w", err)
		}
		listeners = append(listeners, l)
	}

	return listeners, nil
}

// WatchOverseerSignal listens for signals used by overseer and triggers a restart upon receiving them.
// This function launches a new goroutine to listen for signals.
// If sig is nil, SIGUSR1 is used as the default value.
// listeners are the file descriptors to be passed to the child process.
func WatchOverseerSignal(sig os.Signal, listeners []net.Listener) {
	watchSignal := sig
	if watchSignal == nil {
		watchSignal = SIGUSR1
	}

	go func() {
		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh, watchSignal)

		for sig := range signalCh {
			log.Printf("Received signal %v, creating child process", sig)

			// Directly execute a new instance of the current program without using risefront's restart mechanism
			executable, err := os.Executable()
			if err != nil {
				log.Printf("Failed to get executable path: %v", err)
				continue
			}

			// Create a child process
			cmd := exec.Command(executable)

			// Inherit the environment variables of the current process
			cmd.Env = os.Environ()

			// Pass the listeners' file descriptors to the child process
			if len(listeners) > 0 {
				// Set the OVERSEER_NUM_FDS environment variable
				cmd.Env = append(cmd.Env, fmt.Sprintf("OVERSEER_NUM_FDS=%d", len(listeners)))

				// Get the file descriptor for each listener
				cmd.ExtraFiles = make([]*os.File, 0, len(listeners))
				for _, ln := range listeners {
					if tcpLn, ok := ln.(*net.TCPListener); ok {
						file, err := tcpLn.File()
						if err != nil {
							log.Printf("Failed to get TCP listener file descriptor: %v", err)
							continue
						}
						cmd.ExtraFiles = append(cmd.ExtraFiles, file)
					} else if unixLn, ok := ln.(*net.UnixListener); ok {
						file, err := unixLn.File()
						if err != nil {
							log.Printf("Failed to get Unix listener file descriptor: %v", err)
							continue
						}
						cmd.ExtraFiles = append(cmd.ExtraFiles, file)
					}
				}
			}

			// Child process uses the current process's standard input, output, and error
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			// Start the child process
			if err := cmd.Start(); err != nil {
				log.Printf("Failed to create child process: %v", err)
				continue
			}

			log.Printf("Child process started, PID: %d, passed %d listeners", cmd.Process.Pid, len(cmd.ExtraFiles))

			// The current process can continue running and exit gracefully after handling all requests
			// In practice, smooth shutdown logic should be triggered here
		}
	}()
}

// isFromOverseer checks whether the environment variable for overseer file descriptors is set
func isFromOverseer() bool {
	return os.Getenv("OVERSEER_NUM_FDS") != ""
}
