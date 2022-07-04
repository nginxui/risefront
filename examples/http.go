package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"codeberg.org/oliverpool/risefront"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	return risefront.New(ctx, risefront.Config{
		Addresses: []string{":8080"},
		Run: func(l []net.Listener) error {
			s := http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("hello world " + strconv.Itoa(os.Getpid()) + "\n"))
					if r.URL.Path == "/close" {
						time.Sleep(10 * time.Second)
					}
					w.Write([]byte("bye " + strconv.Itoa(os.Getpid())))
				}),
			}
			defer s.Shutdown(context.Background())
			fmt.Println("READY", l[0].Addr())
			defer fmt.Println("BYE", l[0].Addr())
			return s.Serve(l[0])
		},
	})
}
