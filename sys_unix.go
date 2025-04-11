//go:build !windows

package risefront

import (
	"syscall"
)

var (
	SIGUSR1 = syscall.SIGUSR1
	SIGUSR2 = syscall.SIGUSR2
)
