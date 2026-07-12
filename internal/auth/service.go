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

type Service struct {
	store   *Store
	manager *Manager
	oauth   *CodexOAuth
}

func NewService(store *Store, manager *Manager, oauth *CodexOAuth) *Service {
	return &Service{store: store, manager: manager, oauth: oauth}
}

func (s *Service) LoginBrowser(ctx context.Context, onAuthURL func(string)) error {
	credential, err := s.oauth.LoginBrowser(ctx, onAuthURL)
	if err != nil {
		return err
	}
	return s.store.Save(ProviderOpenAICodex, credential)
}

func (s *Service) LoginDevice(ctx context.Context, onDeviceCode func(DeviceCodeInfo)) error {
	credential, err := s.oauth.LoginDevice(ctx, onDeviceCode)
	if err != nil {
		return err
	}
	return s.store.Save(ProviderOpenAICodex, credential)
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
