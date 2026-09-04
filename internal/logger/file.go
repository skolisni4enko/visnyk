package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxLogSize = 5 * 1024 * 1024 // 5 MB

// FileLogger writes structured lines to app.log, thread-safe with simple rotation.
type FileLogger struct {
	mu   sync.Mutex
	path string
	file *os.File
}

// New creates logger at path, ensuring dir exists.
func New(path string) (*FileLogger, error) {
	if path == "" {
		return nil, fmt.Errorf("empty log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("logger mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	return &FileLogger{path: path, file: f}, nil
}

// Path returns file path.
func (l *FileLogger) Path() string { return l.path }

// Log writes level/source/msg with timestamp.
func (l *FileLogger) Log(level, source, msg string) error {
	level = strings.ToUpper(strings.TrimSpace(level))
	if level == "" {
		level = "INFO"
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "app"
	}
	line := fmt.Sprintf("%s [%s] %s: %s\n", time.Now().Format("2006-01-02 15:04:05.000"), level, source, msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		// try reopen
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		l.file = f
	}
	// rotate if needed
	if st, err := l.file.Stat(); err == nil && st.Size() > maxLogSize {
		_ = l.rotateLocked()
	}
	_, err := l.file.WriteString(line)
	if err != nil {
		return err
	}
	_ = l.file.Sync()
	return nil
}

func (l *FileLogger) rotateLocked() error {
	_ = l.file.Close()
	rotated := l.path + ".1"
	_ = os.Remove(rotated)
	_ = os.Rename(l.path, rotated)
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		l.file = nil
		return err
	}
	l.file = f
	_ = l.Log("INFO", "logger", "log rotated, previous -> app.log.1")
	return nil
}

// Close closes file.
func (l *FileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

// Tail returns last n lines (simple, for UI preview).
func (l *FileLogger) Tail(n int) ([]string, error) {
	if n <= 0 {
		n = 50
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= n {
		return lines, nil
	}
	return lines[len(lines)-n:], nil
}
