package auth

import (
	"context"
	"fmt"
	"time"
)

type Status struct {
	Configured bool
	Expired    bool
	ExpiresAt  time.Time
}

type LoginProvider interface {
	Refresher
	LoginBrowser(context.Context, func(string)) (OAuthCredential, error)
	LoginDevice(context.Context, func(DeviceCodeInfo)) (OAuthCredential, error)
}

type Service struct {
	store     *Store
	manager   *Manager
	providers map[string]LoginProvider
}

func NewService(store *Store, manager *Manager) *Service {
	return &Service{store: store, manager: manager, providers: map[string]LoginProvider{}}
}

func (s *Service) Register(provider string, loginProvider LoginProvider) {
	provider = normalizeProvider(provider)
	if provider == "" || loginProvider == nil {
		return
	}
	s.providers[provider] = loginProvider
	if s.manager != nil {
		s.manager.Register(provider, loginProvider)
	}
}

func (s *Service) LoginBrowser(ctx context.Context, provider string, onAuthURL func(string)) error {
	provider = normalizeProvider(provider)
	loginProvider, ok := s.providers[provider]
	if !ok {
		return fmt.Errorf("auth provider %q is not registered", provider)
	}
	credential, err := loginProvider.LoginBrowser(ctx, onAuthURL)
	if err != nil {
		return err
	}
	return s.store.Save(provider, credential)
}

func (s *Service) LoginDevice(ctx context.Context, provider string, onDeviceCode func(DeviceCodeInfo)) error {
	provider = normalizeProvider(provider)
	loginProvider, ok := s.providers[provider]
	if !ok {
		return fmt.Errorf("auth provider %q is not registered", provider)
	}
	credential, err := loginProvider.LoginDevice(ctx, onDeviceCode)
	if err != nil {
		return err
	}
	return s.store.Save(provider, credential)
}

func (s *Service) Status(provider string) (Status, error) {
	credential, ok, err := s.store.Load(provider)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{}, nil
	}
	now := time.Now()
	if s.manager != nil && s.manager.now != nil {
		now = s.manager.now()
	}
	return Status{Configured: true, Expired: !credential.ExpiresAt.After(now), ExpiresAt: credential.ExpiresAt}, nil
}

func (s *Service) Logout(provider string) error {
	return s.store.Delete(provider)
}

func (s *Service) Resolve(ctx context.Context, provider string) (OAuthCredential, error) {
	if s.manager == nil {
		return OAuthCredential{}, fmt.Errorf("auth manager is unavailable")
	}
	return s.manager.Resolve(ctx, provider)
}
