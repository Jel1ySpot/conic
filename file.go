package conic

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// extOf returns the lower-cased extension of path without the leading dot.
func extOf(path string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
}

// readFile reads the config file, mapping errors to conic error types.
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ConfigFileNotFoundError{Path: path, Err: err}
		}
		return nil, ConfigFileReadError{Path: path, Err: err}
	}
	return b, nil
}

// writeFile writes the config file, creating parent directories as needed.
func writeFile(path string, b []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ConfigFileWriteError{Path: path, Err: err}
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return ConfigFileWriteError{Path: path, Err: err}
	}
	return nil
}
