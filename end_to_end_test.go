package risefront

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestMain(m *testing.M) {
	switch os.Getenv("RISEFRONT_TEST_MODE") {
	default:
		// Normal test mode
		os.Exit(m.Run())

	case "dangling-risefront.sock":
		_, err := net.Listen("unix", "risefront.sock")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		os.Exit(0) // exit without closing listener
	}
}

func selfCmd(mode string, arg ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], arg...)
	cmd.Env = []string{"RISEFRONT_TEST_MODE=" + mode}
	return cmd
}

const testAddr = "127.0.0.1:8888"

func TestEndToEnd(t *testing.T) {
	/*
		Start parent instance (which starts 1st child instance).

		Check the 1st (fast) reply from child1.
		Send a slow request to child1.

		Send a signal to parent to trigger upgrade.
		Check the fast reply from child 2.
		And the slow reply.
	*/

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	parentDone := make(chan struct{})
	parentReady := make(chan struct{})
	parentFirstChildDone := make(chan struct{})
	go func() {
		errParent := New(ctx, Config{
			Addresses: []string{testAddr},
			Run: func(l []net.Listener) error {
				assert.Equal(t, 1, len(l))
				close(parentReady)

				s := http.Server{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						t.Log("request parent", r.URL.Path)
						w.Write([]byte("hello world"))
					}),
				}
				defer s.Shutdown(context.Background())
				err := s.Serve(l[0])
				t.Log("parentFirstChild.Serve", err)
				close(parentFirstChildDone)
				return nil
			},
		})
		assert.Check(t, errors.Is(errParent, context.Canceled))
		close(parentDone)
	}()

	select {
	case <-parentReady:
	case <-time.After(time.Second):
		t.Error("parent took too long to be ready")
	}

	resp, err := http.Get("http://" + testAddr)
	assert.NilError(t, err)
	got, err := io.ReadAll(resp.Body)
	assert.NilError(t, err)
	assert.Equal(t, "hello world", string(got))
	err = resp.Body.Close()
	assert.NilError(t, err)

	// create child

	childDone := make(chan struct{})
	childReady := make(chan struct{})
	go func() {
		errChild := New(ctx, Config{
			Addresses: []string{testAddr},
			Run: func(l []net.Listener) error {
				assert.Equal(t, 1, len(l))
				close(childReady)
				t.Log("CHILD READY")

				s := http.Server{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						t.Log("request child", r.URL.Path)
						w.Write([]byte("hello child"))
					}),
				}
				defer s.Shutdown(context.Background())
				err := s.Serve(l[0])
				t.Log("child.Serve", err)
				return nil
			},
		})
		assert.NilError(t, errChild)
		close(childDone)
	}()

	select {
	case <-childReady:
	case <-time.After(time.Second):
		t.Error("child took too long to be ready")
	}

	select {
	case <-parentFirstChildDone:
	case <-time.After(time.Second):
		t.Error("parent first child took too long to close")

	}

	resp, err = http.Get("http://" + testAddr)
	assert.NilError(t, err)
	got, err = io.ReadAll(resp.Body)
	assert.NilError(t, err)
	assert.Equal(t, "hello child", string(got))
	err = resp.Body.Close()
	assert.NilError(t, err)

	cancel()

	select {
	case <-parentDone:
	case <-time.After(time.Second):
		t.Error("parent took too long to shutdown")
	}
}

func TestFirstChildSlowRequest(t *testing.T) {
	os.Remove("risefront.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parentDone := make(chan struct{})
	parentReady := make(chan struct{})
	go func() {
		handlerCalled := uint32(0)
		errParent := New(ctx, Config{
			Addresses: []string{testAddr},
			Run: func(l []net.Listener) error {
				assert.Equal(t, 1, len(l))
				close(parentReady)

				s := http.Server{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						t.Log("request", r.URL.Path)
						w.Write([]byte("hello"))

						if r.URL.Path == "/close" {
							cancel()
							time.Sleep(100 * time.Millisecond)
						}
						w.Write([]byte(" world"))
						t.Log("replied")
						atomic.AddUint32(&handlerCalled, 1)
					}),
				}
				defer s.Shutdown(context.Background())
				err := s.Serve(l[0])
				t.Log("Serve", err)
				return nil
			},
		})
		// ensure handler was called twice, before the parent returned
		assert.Equal(t, uint32(2), atomic.LoadUint32(&handlerCalled))

		assert.Check(t, errors.Is(errParent, context.Canceled))
		close(parentDone)
	}()

	select {
	case <-parentReady:
	case <-time.After(time.Second):
		t.Error("parent took too long to be ready")
	}

	chResp := make(chan string)
	go func() {
		resp, err := http.Get("http://" + testAddr)
		assert.NilError(t, err)
		got, err := io.ReadAll(resp.Body)
		assert.NilError(t, err)
		assert.Equal(t, "hello world", string(got))

		resp, err = http.Get("http://" + testAddr + "/close")
		assert.NilError(t, err)
		got, err = io.ReadAll(resp.Body)
		assert.NilError(t, err)
		chResp <- string(got)
	}()

	select {
	case s := <-chResp:
		assert.Equal(t, "hello world", s)
	case <-time.After(time.Second):
		t.Error("response took too long")
	}

	select {
	case <-parentDone:
	case <-time.After(2 * time.Second):
		t.Error("parent took too long to shutdown")
	}
}

func TestExistingSocket(t *testing.T) {
	os.Remove("risefront.sock")
	cmd := selfCmd("dangling-risefront.sock")
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	parentDone := make(chan struct{})
	parentReady := make(chan struct{})
	go func() {
		errParent := New(ctx, Config{
			Addresses: []string{testAddr},
			Run: func(l []net.Listener) error {
				assert.Equal(t, 1, len(l))
				close(parentReady)
				_, err = l[0].Accept()
				assert.Check(t, errors.Is(err, net.ErrClosed))
				return nil
			},
		})
		assert.Check(t, errors.Is(errParent, context.Canceled))
		close(parentDone)
	}()

	select {
	case <-parentReady:
	case <-time.After(time.Second):
		t.Error("parent took too long to be ready")
	}
	cancel()
	select {
	case <-parentDone:
	case <-time.After(2 * time.Second):
		t.Error("parent took too long to shutdown")
	}
}
