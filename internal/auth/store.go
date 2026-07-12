package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const ProviderOpenAICodex = "openai-codex"

type OAuthCredential struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	ExpiresAt time.Time `json:"expires_at"`
	AccountID string    `json:"account_id"`
}

type credentialFile struct {
	Version   int                        `json:"version"`
	Providers map[string]OAuthCredential `json:"providers"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: filepath.Clean(path)}
}

func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(home, ".config", "liora", "auth.json")), nil
}

func (s *Store) Load(provider string) (OAuthCredential, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readLocked()
	if err != nil {
		return OAuthCredential{}, false, err
	}
	credential, ok := file.Providers[normalizeProvider(provider)]
	return credential, ok, nil
}

func (s *Store) Save(provider string, credential OAuthCredential) error {
	provider = normalizeProvider(provider)
	if provider == "" {
		return fmt.Errorf("auth provider is required")
	}
	if strings.TrimSpace(credential.Access) == "" || strings.TrimSpace(credential.Refresh) == "" || strings.TrimSpace(credential.AccountID) == "" || credential.ExpiresAt.IsZero() {
		return fmt.Errorf("OAuth credential is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readLocked()
	if err != nil {
		return err
	}
	file.Providers[provider] = credential
	return s.writeLocked(file)
}

func (s *Store) Delete(provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := s.readLocked()
	if err != nil {
		return err
	}
	delete(file.Providers, normalizeProvider(provider))
	return s.writeLocked(file)
}

func (s *Store) readLocked() (credentialFile, error) {
	file := credentialFile{Version: 1, Providers: map[string]OAuthCredential{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file, nil
		}
		return credentialFile{}, err
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return credentialFile{}, fmt.Errorf("read auth credentials: %w", err)
	}
	if file.Providers == nil {
		file.Providers = map[string]OAuthCredential{}
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return file, nil
}

func (s *Store) writeLocked(file credentialFile) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(dir, ".auth-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}

func normalizeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "codex" {
		return ProviderOpenAICodex
	}
	return provider
}
