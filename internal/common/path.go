package common

import (
	"os"
	"path/filepath"
)

// AbsPath resolves relative path to absolute via filepath.Abs.
func AbsPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// EnsureDir creates directory for given file path (os.MkdirAll).
func EnsureDir(filePath string) error {
	return os.MkdirAll(filepath.Dir(filePath), 0o755)
}
