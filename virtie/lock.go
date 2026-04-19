package virtie

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type FileLocker struct{}

func (l *FileLocker) Acquire(path string) (Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory for %q: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire lock %q: %w", path, err)
	}

	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write lock %q: %w", path, err)
	}

	return &fileLock{path: path, file: file}, nil
}

type fileLock struct {
	path string
	file *os.File
}

func (l *fileLock) Release() error {
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return fmt.Errorf("close lock %q: %w", l.path, err)
		}
		l.file = nil
	}

	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove lock %q: %w", l.path, err)
	}

	return nil
}
