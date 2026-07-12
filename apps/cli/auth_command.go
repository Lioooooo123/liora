package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	authpkg "github.com/Lioooooo123/liora/internal/auth"
	"github.com/Lioooooo123/liora/internal/tui"
)

type codexAuthenticator interface {
	LoginBrowser(context.Context, func(string)) error
	LoginDevice(context.Context, func(authpkg.DeviceCodeInfo)) error
	Status(string) (authpkg.Status, error)
	Logout(string) error
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
			err := service.LoginDevice(ctx, func(info authpkg.DeviceCodeInfo) {
				fmt.Fprintf(output, "Open %s and enter code %s\n", info.VerificationURL, info.UserCode)
			})
			if err != nil {
				return true, err
			}
		} else {
			err := service.LoginBrowser(ctx, func(url string) {
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
	models  tui.CommandHandler
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
		if err := c.service.LoginBrowser(ctx, func(string) {}); err != nil {
			return "", true, err
		}
		result := "Logged in to OpenAI Codex."
		if c.models != nil {
			modelResult, handled, err := c.models.HandleCommand(ctx, "/model set openai-codex gpt-5.4")
			if err != nil {
				return "", true, err
			}
			if handled && strings.TrimSpace(modelResult) != "" {
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

func codexAuthReport(status authpkg.Status, err error) string {
	if err != nil {
		return "unavailable"
	}
	if !status.Configured {
		return "missing"
	}
	if status.Expired {
		return "expired"
	}
	return "configured"
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
