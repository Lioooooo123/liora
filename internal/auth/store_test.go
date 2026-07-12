package auth

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRefresher struct {
	calls int
	got   string
	next  OAuthCredential
}

func (f *fakeRefresher) Refresh(_ context.Context, refreshToken string) (OAuthCredential, error) {
	f.calls++
	f.got = refreshToken
	return f.next, nil
}

func TestStorePersistsAndRemovesCodexOAuthCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "auth.json")
	store := NewStore(path)
	want := OAuthCredential{
		Access:    "access-token",
		Refresh:   "refresh-token",
		ExpiresAt: time.Unix(1_900_000_000, 0).UTC(),
		AccountID: "account-123",
	}

	if err := store.Save(ProviderOpenAICodex, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Load(ProviderOpenAICodex)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Fatalf("unexpected stored credential ok=%t got=%#v want=%#v", ok, got, want)
	}

	dirInfo, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	assertPathMode(t, dirInfo, 0o700)
	assertPathMode(t, path, 0o600)

	if err := store.Delete(ProviderOpenAICodex); err != nil {
		t.Fatal(err)
	}
	_, ok, err = store.Load(ProviderOpenAICodex)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected credential to be removed")
	}
}

func TestManagerRefreshesExpiredCredentialAndPersistsReplacement(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.Save(ProviderOpenAICodex, OAuthCredential{
		Access:    "expired-access",
		Refresh:   "old-refresh",
		ExpiresAt: now.Add(-time.Minute),
		AccountID: "old-account",
	}); err != nil {
		t.Fatal(err)
	}
	refresher := &fakeRefresher{next: OAuthCredential{
		Access:    "fresh-access",
		Refresh:   "fresh-refresh",
		ExpiresAt: now.Add(time.Hour),
		AccountID: "fresh-account",
	}}
	manager := NewManager(store, ManagerOptions{Now: func() time.Time { return now }})
	manager.Register(ProviderOpenAICodex, refresher)

	got, err := manager.Resolve(context.Background(), ProviderOpenAICodex)
	if err != nil {
		t.Fatal(err)
	}
	if got != refresher.next || refresher.calls != 1 || refresher.got != "old-refresh" {
		t.Fatalf("unexpected refresh result got=%#v calls=%d token=%q", got, refresher.calls, refresher.got)
	}
	persisted, ok, err := store.Load(ProviderOpenAICodex)
	if err != nil || !ok || persisted != refresher.next {
		t.Fatalf("replacement not persisted ok=%t got=%#v err=%v", ok, persisted, err)
	}
}

type slowRefresher struct {
	calls atomic.Int32
	next  OAuthCredential
}

func (s *slowRefresher) Refresh(_ context.Context, _ string) (OAuthCredential, error) {
	s.calls.Add(1)
	time.Sleep(75 * time.Millisecond)
	return s.next, nil
}

func TestManagersCoordinateRefreshAcrossInstances(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err := store.Save(ProviderOpenAICodex, OAuthCredential{
		Access: "expired", Refresh: "refresh", ExpiresAt: now.Add(-time.Minute), AccountID: "account",
	}); err != nil {
		t.Fatal(err)
	}
	refresher := &slowRefresher{next: OAuthCredential{
		Access: "fresh", Refresh: "fresh-refresh", ExpiresAt: now.Add(time.Hour), AccountID: "account",
	}}
	managers := []*Manager{
		NewManager(store, ManagerOptions{Now: func() time.Time { return now }}),
		NewManager(store, ManagerOptions{Now: func() time.Time { return now }}),
	}
	for _, manager := range managers {
		manager.Register(ProviderOpenAICodex, refresher)
	}
	start := make(chan struct{})
	errs := make(chan error, len(managers))
	var wg sync.WaitGroup
	for _, manager := range managers {
		wg.Add(1)
		go func(manager *Manager) {
			defer wg.Done()
			<-start
			_, err := manager.Resolve(context.Background(), ProviderOpenAICodex)
			errs <- err
		}(manager)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := refresher.calls.Load(); got != 1 {
		t.Fatalf("refresh calls=%d want=1", got)
	}
}

func assertPathMode(t *testing.T, path string, want uint32) {
	t.Helper()
	info, err := filepath.Glob(path)
	if err != nil || len(info) != 1 {
		t.Fatalf("path %s missing: matches=%v err=%v", path, info, err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := uint32(stat.Mode().Perm()); got != want {
		t.Fatalf("mode for %s = %04o, want %04o", path, got, want)
	}
}
