package risefront

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestMain(m *testing.M) {
	switch os.Getenv("RISEFRONT_TEST_MODE") {
	default:
		// Normal test mode
		os.Exit(m.Run())

	case "run":
		// TODO call equivalent of main
		fmt.Println("run", os.Getpid())
	}
}

func runSelf(mode string, arg ...string) ([]byte, error) {
	cmd := exec.Command(os.Args[0], arg...)
	cmd.Env = []string{"RISEFRONT_TEST_MODE=" + mode}
	return cmd.CombinedOutput()
}

func TestEndToEnd(t *testing.T) {
	// go func() {
	out, err := runSelf("run", "lol")
	t.Log(string(out), err)
	// }()
	t.Log("ok", os.Getpid())
	t.Fail()
}
