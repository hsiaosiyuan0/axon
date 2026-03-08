// Package watch — daemon.go
// Provides helpers for running axon watch as a background daemon process,
// including PID file management and log file routing.
package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// DefaultLogPath returns the default watch log path: ~/.axon/watch.log
func DefaultLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".axon", "watch.log")
}

// DefaultPIDPath returns the default PID file path: ~/.axon/watch.pid
func DefaultPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".axon", "watch.pid")
}

// WritePID writes the current process PID to the given file.
func WritePID(pidFile string) error {
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

// RemovePID deletes the PID file (call on clean shutdown).
func RemovePID(pidFile string) {
	_ = os.Remove(pidFile)
}

// ReadPID reads and returns the PID stored in the file.
// Returns 0 and an error if the file is absent or invalid.
func ReadPID(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid PID in %s: %w", pidFile, err)
	}
	return pid, nil
}

// IsRunning checks whether the process recorded in pidFile is alive.
func IsRunning(pidFile string) (int, bool) {
	pid, err := ReadPID(pidFile)
	if err != nil {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	// Signal 0 just checks existence without killing.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0, false
	}
	return pid, true
}

// StopDaemon sends SIGTERM to the daemon recorded in pidFile.
func StopDaemon(pidFile string) error {
	pid, running := IsRunning(pidFile)
	if !running {
		return fmt.Errorf("no running watch daemon found (checked %s)", pidFile)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal process %d: %w", pid, err)
	}
	fmt.Printf("🛑 Sent SIGTERM to watch daemon (PID %d)\n", pid)
	return nil
}

// OpenLogFile opens (or creates) the log file for appending.
// Returns the file and a cleanup func.
func OpenLogFile(logPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
