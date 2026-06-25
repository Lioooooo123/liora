package permission

import (
	"context"
	"fmt"
	"strings"
)

type Mode string

const (
	ModeAuto   Mode = "auto"
	ModePrompt Mode = "prompt"
)

type Request struct {
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Risk   string `json:"risk"`
	Reason string `json:"reason"`
}

type Checker interface {
	Check(context.Context, Request) error
}

type CheckerFunc func(context.Context, Request) error

func (f CheckerFunc) Check(ctx context.Context, request Request) error {
	return f(ctx, request)
}

type RequiredError struct {
	Request Request
}

func (e *RequiredError) Error() string {
	if e.Request.Reason == "" {
		return fmt.Sprintf("permission required for %s %s", e.Request.Tool, e.Request.Input)
	}
	return fmt.Sprintf("permission required for %s %s: %s", e.Request.Tool, e.Request.Input, e.Request.Reason)
}

type Policy struct {
	Mode                   Mode
	Approved               bool
	AllowWritesInPatchMode bool
}

func (p Policy) Check(_ context.Context, request Request) error {
	if p.Approved || p.Mode != ModePrompt {
		return nil
	}
	classified, required := Classify(request.Tool, request.Input, p.AllowWritesInPatchMode)
	if !required {
		return nil
	}
	return &RequiredError{Request: classified}
}

func Classify(tool string, input string, allowWritesInPatchMode bool) (Request, bool) {
	tool = strings.ToLower(strings.TrimSpace(tool))
	input = strings.TrimSpace(input)
	switch tool {
	case "write", "append", "edit", "replace", "mkdir", "delete":
		if allowWritesInPatchMode {
			return Request{}, false
		}
		return Request{Tool: tool, Input: input, Risk: "write", Reason: "This step changes files in the workspace."}, true
	case "run":
		if reason := dangerousShellReason(input); reason != "" {
			return Request{Tool: tool, Input: input, Risk: "dangerous_shell", Reason: reason}, true
		}
	case "mcp":
		return Request{Tool: tool, Input: input, Risk: "external", Reason: "This step calls an external MCP tool."}, true
	}
	return Request{}, false
}

func dangerousShellReason(command string) string {
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(command), " ")) + " "
	patterns := map[string]string{
		" rm -rf ":          "Command contains rm -rf.",
		" sudo ":            "Command uses sudo.",
		" chmod -r ":        "Command recursively changes permissions.",
		" chown -r ":        "Command recursively changes ownership.",
		" git reset --hard": "Command can discard git changes.",
		" git clean -fd":    "Command can delete untracked files.",
		" dd ":              "Command can overwrite raw devices or files.",
		" mkfs":             "Command can format storage.",
		" shutdown ":        "Command can stop the machine.",
		" reboot ":          "Command can restart the machine.",
		" kill -9 ":         "Command force-kills processes.",
	}
	for pattern, reason := range patterns {
		if strings.Contains(normalized, pattern) {
			return reason
		}
	}
	if strings.Contains(normalized, " curl ") && strings.Contains(normalized, "| sh") {
		return "Command pipes downloaded content into a shell."
	}
	if strings.Contains(normalized, " wget ") && strings.Contains(normalized, "| sh") {
		return "Command pipes downloaded content into a shell."
	}
	return ""
}
