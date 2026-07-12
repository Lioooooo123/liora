package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Refresher interface {
	Refresh(context.Context, string) (OAuthCredential, error)
}

type ManagerOptions struct {
	Now         func() time.Time
	RefreshSkew time.Duration
}

type Manager struct {
	store       *Store
	now         func() time.Time
	refreshSkew time.Duration
	mu          sync.Mutex
	refreshers  map[string]Refresher
}

func NewManager(store *Store, options ManagerOptions) *Manager {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	refreshSkew := options.RefreshSkew
	if refreshSkew == 0 {
		refreshSkew = time.Minute
	}
	return &Manager{
		store:       store,
		now:         now,
		refreshSkew: refreshSkew,
		refreshers:  map[string]Refresher{},
	}
}

func (m *Manager) Register(provider string, refresher Refresher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	provider = normalizeProvider(provider)
	if provider == "" || refresher == nil {
		return
	}
	m.refreshers[provider] = refresher
}

func (m *Manager) Resolve(ctx context.Context, provider string) (OAuthCredential, error) {
	provider = normalizeProvider(provider)
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, err := acquireCredentialLock(m.store.path + ".lock")
	if err != nil {
		return OAuthCredential{}, err
	}
	defer lock.Unlock()
	credential, ok, err := m.store.Load(provider)
	if err != nil {
		return OAuthCredential{}, err
	}
	if !ok {
		return OAuthCredential{}, fmt.Errorf("%s authentication is not configured; run `liora auth login codex`", strings.TrimSpace(provider))
	}
	if credential.ExpiresAt.After(m.now().Add(m.refreshSkew)) {
		return credential, nil
	}
	refresher := m.refreshers[provider]
	if refresher == nil {
		return OAuthCredential{}, fmt.Errorf("%s OAuth token expired and no refresher is registered", provider)
	}
	refreshed, err := refresher.Refresh(ctx, credential.Refresh)
	if err != nil {
		return OAuthCredential{}, fmt.Errorf("refresh %s authentication: %w", provider, err)
	}
	if err := m.store.Save(provider, refreshed); err != nil {
		return OAuthCredential{}, err
	}
	return refreshed, nil
}
