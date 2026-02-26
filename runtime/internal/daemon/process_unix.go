//go:build !windows

package daemon

import (
	"fmt"
	"syscall"
	"time"
)

// IsRunning checks if a process with the given PID is still alive.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

// StopProcess sends SIGTERM, waits up to timeout for exit, then SIGKILL.
func StopProcess(pid int, timeout time.Duration) error {
	if !IsRunning(pid) {
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsRunning(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Force kill.
	if IsRunning(pid) {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("send SIGKILL: %w", err)
		}
	}
	return nil
}
