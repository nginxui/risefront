package risefront_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"code.pfad.fr/risefront"
)

func newServer() *http.Server {
	// This is an example server, which shows the PID of its process.
	// Start processes in parallel to show the forwarding of the
	// incoming connection to the latest process.
	return &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("hello world " + strconv.Itoa(os.Getpid()) + "\n"))
			if r.URL.Path == "/hang" {
				time.Sleep(10 * time.Second)
			}
			w.Write([]byte("bye " + strconv.Itoa(os.Getpid())))
		}),
	}
}

func Example_http() {
	// In production, use signal.NotifyContext to interrupt on Ctrl+C:
	// ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := risefront.New(ctx, risefront.Config{
		Addresses: []string{":8080"},
		Run: func(l []net.Listener) error {
			// Example http.Server
			s := newServer()

			// defer Shutdown to wait for ongoing requests to be served before returning
			defer s.Shutdown(context.Background())
			return s.Serve(l[0]) // serve on the given listener
		},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		panic(err)
	}
	// Output:
}
