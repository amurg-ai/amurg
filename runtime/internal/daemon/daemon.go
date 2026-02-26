// Package daemon provides helpers for running the runtime as a background process.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultDir returns the default directory for daemon files (~/.amurg/).
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".amurg"
	}
	return filepath.Join(home, ".amurg")
}

// PIDPath returns the path to the PID file.
func PIDPath() string {
	return filepath.Join(DefaultDir(), "runtime.pid")
}

// LogPath returns the path to the log file.
func LogPath() string {
	return filepath.Join(DefaultDir(), "runtime.log")
}

// SocketPath returns the path to the IPC Unix socket.
func SocketPath() string {
	return filepath.Join(DefaultDir(), "runtime.sock")
}

// WritePID writes the given PID to the PID file.
func WritePID(pid int) error {
	dir := DefaultDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create daemon dir: %w", err)
	}
	return os.WriteFile(PIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0600)
}

// ReadPID reads the PID from the PID file. Returns 0 if the file doesn't exist.
func ReadPID() (int, error) {
	data, err := os.ReadFile(PIDPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// RemovePID removes the PID file.
func RemovePID() error {
	err := os.Remove(PIDPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// OpenLogFile opens or creates the log file for appending.
func OpenLogFile() (*os.File, error) {
	dir := DefaultDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create daemon dir: %w", err)
	}
	return os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}
