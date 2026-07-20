//go:build unix

package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type credentialLock struct {
	file *os.File
}

func acquireCredentialLock(path string) (*credentialLock, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("lock auth credentials: %w", err)
	}
	return &credentialLock{file: file}, nil
}

func (l *credentialLock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}
