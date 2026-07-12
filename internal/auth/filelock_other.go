//go:build !unix

package auth

import "sync"

var fallbackCredentialMutex sync.Mutex

type credentialLock struct{}

func acquireCredentialLock(string) (*credentialLock, error) {
	fallbackCredentialMutex.Lock()
	return &credentialLock{}, nil
}

func (l *credentialLock) Unlock() {
	fallbackCredentialMutex.Unlock()
}
