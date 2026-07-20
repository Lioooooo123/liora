package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	authpkg "github.com/Lioooooo123/liora/internal/auth"
	"github.com/Lioooooo123/liora/internal/llm"
)

type codexAuthenticator interface {
	LoginBrowser(context.Context, string, func(string)) error
	LoginDevice(context.Context, string, func(authpkg.DeviceCodeInfo)) error
	Status(string) (authpkg.Status, error)
	Logout(string) error
}

type modelSelector interface {
	SelectModel(context.Context, string, string) (string, error)
}

func handleAuthCommand(ctx context.Context, args []string, service codexAuthenticator, output io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "auth" {
		return false, nil
	}
	if service == nil {
		return true, fmt.Errorf("auth service is unavailable")
	}
	if len(args) == 1 || args[1] == "status" {
		status, err := service.Status(authpkg.ProviderOpenAICodex)
		if err != nil {
			return true, err
		}
		fmt.Fprintln(output, formatCodexAuthStatus(status))
		return true, nil
	}
	provider := "codex"
	if len(args) >= 3 {
		provider = args[2]
	}
	if !isCodexProvider(provider) {
		return true, fmt.Errorf("unsupported auth provider %q; only codex is supported", provider)
	}
	switch args[1] {
	case "login":
		device := containsArg(args[3:], "--device")
		if device {
			err := service.LoginDevice(ctx, authpkg.ProviderOpenAICodex, func(info authpkg.DeviceCodeInfo) {
				fmt.Fprintf(output, "Open %s and enter code %s\n", info.VerificationURL, info.UserCode)
			})
			if err != nil {
				return true, err
			}
		} else {
			err := service.LoginBrowser(ctx, authpkg.ProviderOpenAICodex, func(url string) {
				fmt.Fprintln(output, "Opening browser for OpenAI Codex login:")
				fmt.Fprintln(output, url)
			})
			if err != nil {
				return true, err
			}
		}
		fmt.Fprintln(output, "Logged in to OpenAI Codex.")
		return true, nil
	case "logout":
		if err := service.Logout(authpkg.ProviderOpenAICodex); err != nil {
			return true, err
		}
		fmt.Fprintln(output, "Logged out of OpenAI Codex.")
		return true, nil
	default:
		return true, fmt.Errorf("usage: liora auth <login codex [--device]|status|logout codex>")
	}
}

type codexAuthCommand struct {
	service codexAuthenticator
	models  modelSelector
}

func (c codexAuthCommand) HandleCommand(ctx context.Context, line string) (string, bool, error) {
	line = strings.TrimSpace(line)
	switch {
	case line == "/auth" || line == "/auth status":
		status, err := c.service.Status(authpkg.ProviderOpenAICodex)
		return formatCodexAuthStatus(status), true, err
	case line == "/logout" || line == "/logout codex" || line == "/logout openai-codex":
		if err := c.service.Logout(authpkg.ProviderOpenAICodex); err != nil {
			return "", true, err
		}
		return "Logged out of OpenAI Codex.", true, nil
	case line == "/login" || line == "/login codex" || line == "/login openai-codex":
		if err := c.service.LoginBrowser(ctx, authpkg.ProviderOpenAICodex, nil); err != nil {
			return "", true, fmt.Errorf("%w; for headless login run `liora auth login codex --device` in another terminal", err)
		}
		result := "Logged in to OpenAI Codex."
		if c.models != nil {
			modelResult, err := c.models.SelectModel(ctx, llm.ProviderOpenAICodex, llm.DefaultOpenAICodexModel)
			if err != nil {
				return "", true, err
			}
			if strings.TrimSpace(modelResult) != "" {
				result += "\n" + modelResult
			}
		}
		return result, true, nil
	default:
		return "", false, nil
	}
}

func formatCodexAuthStatus(status authpkg.Status) string {
	if !status.Configured {
		return "OpenAI Codex authentication: not configured"
	}
	if status.Expired {
		return "OpenAI Codex authentication: configured (token expired; refresh will run on next request)"
	}
	return "OpenAI Codex authentication: configured"
}

func codexAuthReport(status authpkg.Status, err error) *authpkg.Status {
	if err != nil {
		return nil
	}
	return &status
}

func providerAuthReport(provider string, status authpkg.Status, err error) map[string]*authpkg.Status {
	report := codexAuthReport(status, err)
	if report == nil {
		return nil
	}
	return map[string]*authpkg.Status{llm.NormalizeProvider(provider): report}
}

func isCodexProvider(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	return provider == "codex" || provider == authpkg.ProviderOpenAICodex
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
