package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	authpkg "github.com/Lioooooo123/liora/internal/auth"
	"github.com/Lioooooo123/liora/internal/tui"
)

type fakeCodexAuthenticator struct {
	configured bool
	method     string
	loggedOut  bool
}

func (f *fakeCodexAuthenticator) LoginBrowser(_ context.Context, onURL func(string)) error {
	f.method = "browser"
	onURL("https://auth.example/authorize")
	f.configured = true
	return nil
}

func (f *fakeCodexAuthenticator) LoginDevice(_ context.Context, onCode func(authpkg.DeviceCodeInfo)) error {
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
	modelCommand := ""
	models := tui.CommandHandlerFunc(func(_ context.Context, line string) (string, bool, error) {
		modelCommand = line
		return "model updated", true, nil
	})
	handler := codexAuthCommand{service: service, models: models}

	result, handled, err := handler.HandleCommand(t.Context(), "/login codex")
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if service.method != "browser" || modelCommand != "/model set openai-codex gpt-5.4" {
		t.Fatalf("method=%q model_command=%q", service.method, modelCommand)
	}
	if !strings.Contains(result, "Logged in to OpenAI Codex") || !strings.Contains(result, "model updated") {
		t.Fatalf("unexpected result %q", result)
	}
}
