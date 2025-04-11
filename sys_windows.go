//go:build windows

package risefront

import (
	"syscall"
)

var (
	// On Windows, use SIGTERM instead of SIGUSR1/SIGUSR2
	SIGUSR1 = syscall.SIGTERM
	SIGUSR2 = syscall.SIGTERM
)
