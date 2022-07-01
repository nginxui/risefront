package risefront

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestMain(m *testing.M) {
	switch os.Getenv("RISEFRONT_TEST_MODE") {
	default:
		// Normal test mode
		os.Exit(m.Run())

	case "parent":
		// cfg := Config{
		// 	// SocketPrefix: "testing-",
		// 	Addresses: []string{"8888"},
		// 	Run: func(l []net.Listener) error {
		// 		fmt.Println("LISTENT", l)
		// 		return nil
		// 	},
		// }
		// New(cfg)
		// TODO call equivalent of main
	}
}

func selfCmd(mode string, arg ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], arg...)
	cmd.Env = []string{"RISEFRONT_TEST_MODE=" + mode}
	return cmd
}

func TestEndToEnd(t *testing.T) {
	/*
		Start parent instance (which starts 1st child instance).

		Check the 1st (fast) reply from child1.
		Send a slow request to child1.

		Send a signal to parent to trigger upgrade.
		Check the fast reply from child 2.
		And the slow reply.
	*/

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parentDone := make(chan struct{})
	go func() {
		errParent := New(ctx, Config{
			Addresses: []string{":8888"},
			Run: func(l []net.Listener) error {
				fmt.Println("LISTENT", l)
				return nil
			},
		})
		assert.NilError(t, errParent)
		close(parentDone)
	}()

	select {
	case <-parentDone:
	case <-time.After(3 * time.Second):
		t.Error("parent took too long to shutdown")
	}
}
