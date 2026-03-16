package risefront

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func TestErrorIsNobodyListening(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "direct connect refused",
			err: &os.SyscallError{
				Syscall: "connect",
				Err:     syscall.ECONNREFUSED,
			},
			want: true,
		},
		{
			name: "wrapped connect refused",
			err: &net.OpError{
				Op:  "dial",
				Net: "unix",
				Err: &os.SyscallError{
					Syscall: "connect",
					Err:     syscall.ECONNREFUSED,
				},
			},
			want: true,
		},
		{
			name: "socket missing",
			err: &os.SyscallError{
				Syscall: "connect",
				Err:     os.ErrNotExist,
			},
			want: false,
		},
		{
			name: "different syscall",
			err: &os.SyscallError{
				Syscall: "read",
				Err:     syscall.ECONNREFUSED,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errorIsNobodyListening(tc.err); got != tc.want {
				t.Fatalf("errorIsNobodyListening(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
