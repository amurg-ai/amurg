//go:build windows

package daemon

import "syscall"

// DetachSysProcAttr returns SysProcAttr for launching a detached child process.
func DetachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
