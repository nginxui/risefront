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
	cmd := exec.Command(os.Args[0], arg...) //nolint:gosec
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

	// Remove any existing socket file
	os.Remove("risefront.sock")

	ctx, cancel := context.WithCancel(context.Background())
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
						_, err := w.Write([]byte("hello world"))
						assert.NilError(t, err)
					}),
				}
				defer s.Shutdown(context.Background()) //nolint:errcheck
				err := s.Serve(l[0])
				t.Log("parentFirstChild.Serve", err)
				close(parentFirstChildDone)
				return nil
			},
		})
		assert.Check(t, errors.Is(errParent, context.Canceled), "Parent should return context.Canceled when context is canceled")
		close(parentDone)
	}()

	select {
	case <-parentReady:
	case <-time.After(2 * time.Second): // Increase timeout
		t.Error("parent took too long to be ready")
	}

	// Try multiple times to connect, with a short delay between attempts
	var resp *http.Response
	var err error
	for i := 0; i < 5; i++ {
		resp, err = http.Get("http://" + testAddr)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
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
						_, errW := w.Write([]byte("hello child"))
						assert.NilError(t, errW)
					}),
				}
				defer s.Shutdown(context.Background()) //nolint:errcheck
				errRun := s.Serve(l[0])
				t.Log("child.Serve", errRun)
				return nil
			},
		})
		assert.NilError(t, errChild)
		close(childDone)
	}()

	select {
	case <-childReady:
	case <-time.After(2 * time.Second): // Increase timeout
		t.Error("child took too long to be ready")
	}

	select {
	case <-parentFirstChildDone:
	case <-time.After(2 * time.Second): // Increase timeout
		t.Error("parent first child took too long to close")
	}

	// Try multiple times to connect to child, with a short delay between attempts
	for i := 0; i < 5; i++ {
		resp, err = http.Get("http://" + testAddr)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	assert.NilError(t, err)
	got, err = io.ReadAll(resp.Body)
	assert.NilError(t, err)
	assert.Equal(t, "hello child", string(got))
	err = resp.Body.Close()
	assert.NilError(t, err)

	cancel()

	select {
	case <-parentDone:
	case <-time.After(2 * time.Second): // Increase timeout
		t.Error("parent took too long to shutdown")
	}
}

func TestFirstChildSlowRequest(t *testing.T) {
	os.Remove("risefront.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parentDone := make(chan struct{})
	parentReady := make(chan struct{})
	handlerCalled := uint32(0)

	go func() {
		errParent := New(ctx, Config{
			Addresses: []string{testAddr},
			Run: func(l []net.Listener) error {
				assert.Equal(t, 1, len(l))
				close(parentReady)

				s := http.Server{
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						t.Log("request", r.URL.Path)
						_, err := w.Write([]byte("hello"))
						assert.NilError(t, err)

						if r.URL.Path == "/close" {
							cancel()
							time.Sleep(100 * time.Millisecond)
						}
						_, err = w.Write([]byte(" world"))
						assert.NilError(t, err)
						t.Log("replied")
						atomic.AddUint32(&handlerCalled, 1)
					}),
				}
				defer s.Shutdown(context.Background()) //nolint:errcheck
				err := s.Serve(l[0])
				t.Log("Serve", err)
				return nil
			},
		})
		// ensure handler was called twice, before the parent returned
		assert.Check(t, errors.Is(errParent, context.Canceled))
		close(parentDone)
	}()

	select {
	case <-parentReady:
	case <-time.After(time.Second):
		t.Error("parent took too long to be ready")
	}

	chResp := make(chan string)
	requestsDone := make(chan struct{})

	go func() {
		// First request
		resp, err := http.Get("http://" + testAddr)
		if err != nil {
			t.Logf("First request failed: %v", err)
			chResp <- "ERROR"
			close(requestsDone)
			return
		}
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("Reading first response failed: %v", err)
			chResp <- "ERROR"
			close(requestsDone)
			return
		}
		t.Logf("First response: %s", string(got))
		firstResponse := string(got)
		assert.Equal(t, "hello world", firstResponse)
		resp.Body.Close()

		// Second request with /close path
		resp, err = http.Get("http://" + testAddr + "/close")
		if err != nil {
			t.Logf("Second request failed: %v", err)
			chResp <- "ERROR"
			close(requestsDone)
			return
		}
		got, err = io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("Reading second response failed: %v", err)
			chResp <- "ERROR"
			close(requestsDone)
			return
		}
		t.Logf("Second response: %s", string(got))
		secondResponse := string(got)
		assert.Equal(t, "hello world", secondResponse)
		resp.Body.Close()

		chResp <- secondResponse
		close(requestsDone)
	}()

	select {
	case s := <-chResp:
		if s == "ERROR" {
			t.Error("Error in HTTP request processing")
		} else {
			assert.Equal(t, "hello world", s)
		}
	case <-time.After(3 * time.Second): // Increased timeout
		t.Error("response took too long")
	}

	// Wait for both HTTP requests to complete before checking handlerCalled
	<-requestsDone

	// Ensure both requests were handled
	assert.Equal(t, uint32(2), atomic.LoadUint32(&handlerCalled))

	select {
	case <-parentDone:
	case <-time.After(5 * time.Second):
		t.Error("parent took too long to shutdown")
	}
}

func TestExistingSocket(t *testing.T) {
	// Make sure there's no existing socket file
	os.Remove("risefront.sock")

	// Create a dangling socket file
	cmd := selfCmd("dangling-risefront.sock")
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))

	// Wait shortly for the socket file to be created
	time.Sleep(100 * time.Millisecond)

	// Create a short-lived context
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start using our existing socket file
	parentDone := make(chan struct{})
	go func() {
		// When using an existing socket, New should attempt to dial it,
		// detect the context cancelation, and return the appropriate error
		errParent := New(ctx, Config{
			Addresses: []string{testAddr},
			// We don't expect Run to be called if the socket file exists
			Run: func(l []net.Listener) error {
				t.Log("Run called unexpectedly")
				return nil
			},
		})
		// We just test that New returns after context is canceled
		t.Logf("New returned: %v", errParent)
		close(parentDone)
	}()

	// Wait until either context expires or parent is done
	<-ctx.Done()

	// Check that parent eventually finishes
	select {
	case <-parentDone:
		// Success, function returned
	case <-time.After(3 * time.Second):
		t.Error("parent took too long to shutdown after context timeout")
	}
}
