//go:build windows

package daemon

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

const processQueryLimitedInformation = 0x1000

// IsRunning checks if a process with the given PID is still alive.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(h)
	return true
}

// StopProcess kills the process and waits up to timeout for it to exit.
func StopProcess(pid int, timeout time.Duration) error {
	if !IsRunning(pid) {
		return nil
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	// Windows has no SIGTERM; kill directly.
	if err := p.Kill(); err != nil {
		return fmt.Errorf("kill process: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsRunning(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}
