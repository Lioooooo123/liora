package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	authpkg "github.com/Lioooooo123/liora/internal/auth"
)

type fakeCodexAuthenticator struct {
	configured bool
	method     string
	loggedOut  bool
	browserErr error
}

type fakeModelSelector struct {
	provider string
	model    string
}

func (f *fakeModelSelector) SelectModel(_ context.Context, provider string, model string) (string, error) {
	f.provider = provider
	f.model = model
	return "model updated", nil
}

func (f *fakeCodexAuthenticator) LoginBrowser(_ context.Context, _ string, onURL func(string)) error {
	f.method = "browser"
	if f.browserErr != nil {
		return f.browserErr
	}
	if onURL != nil {
		onURL("https://auth.example/authorize")
	}
	f.configured = true
	return nil
}

func TestTUIAuthLoginReportsDeviceCodeFallbackWhenBrowserCannotOpen(t *testing.T) {
	service := &fakeCodexAuthenticator{browserErr: errors.New("browser opener unavailable")}
	handler := codexAuthCommand{service: service}

	_, handled, err := handler.HandleCommand(t.Context(), "/login codex")
	if !handled || err == nil || !strings.Contains(err.Error(), "liora auth login codex --device") {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
}

func (f *fakeCodexAuthenticator) LoginDevice(_ context.Context, _ string, onCode func(authpkg.DeviceCodeInfo)) error {
	f.method = "device"
	onCode(authpkg.DeviceCodeInfo{UserCode: "ABCD-EFGH", VerificationURL: "https://auth.example/device"})
	f.configured = true
	return nil
}

func (f *fakeCodexAuthenticator) Status(string) (authpkg.Status, error) {
	return authpkg.Status{Configured: f.configured}, nil
}

func (f *fakeCodexAuthenticator) Logout(string) error {
	f.loggedOut = true
	f.configured = false
	return nil
}

func TestCLIAuthDeviceLoginStatusAndLogout(t *testing.T) {
	service := &fakeCodexAuthenticator{}
	var output bytes.Buffer

	handled, err := handleAuthCommand(t.Context(), []string{"auth", "login", "codex", "--device"}, service, &output)
	if err != nil || !handled {
		t.Fatalf("login handled=%t err=%v", handled, err)
	}
	if service.method != "device" || !strings.Contains(output.String(), "ABCD-EFGH") || !strings.Contains(output.String(), "Logged in to OpenAI Codex") {
		t.Fatalf("unexpected device login method=%q output=%q", service.method, output.String())
	}

	output.Reset()
	_, err = handleAuthCommand(t.Context(), []string{"auth", "status"}, service, &output)
	if err != nil || !strings.Contains(output.String(), "configured") {
		t.Fatalf("unexpected status output=%q err=%v", output.String(), err)
	}

	output.Reset()
	_, err = handleAuthCommand(t.Context(), []string{"auth", "logout", "codex"}, service, &output)
	if err != nil || !service.loggedOut || !strings.Contains(output.String(), "Logged out") {
		t.Fatalf("unexpected logout output=%q err=%v", output.String(), err)
	}
}

func TestTUIAuthLoginUsesBrowserAndSwitchesCurrentThreadToCodex(t *testing.T) {
	service := &fakeCodexAuthenticator{}
	models := &fakeModelSelector{}
	handler := codexAuthCommand{service: service, models: models}

	result, handled, err := handler.HandleCommand(t.Context(), "/login codex")
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if service.method != "browser" || models.provider != "openai-codex" || models.model != "gpt-5.4" {
		t.Fatalf("method=%q provider=%q model=%q", service.method, models.provider, models.model)
	}
	if !strings.Contains(result, "Logged in to OpenAI Codex") || !strings.Contains(result, "model updated") {
		t.Fatalf("unexpected result %q", result)
	}
}
