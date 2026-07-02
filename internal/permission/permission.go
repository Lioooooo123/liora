package permission

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Lioooooo123/liora/internal/trust"
)

type Mode string

const (
	ModeAuto   Mode = "auto"
	ModePrompt Mode = "prompt"
)

type Request struct {
	Tool          string `json:"tool"`
	Input         string `json:"input"`
	Risk          string `json:"risk"`
	Reason        string `json:"reason"`
	Trust         string `json:"trust,omitempty"`
	ContentSource string `json:"content_source,omitempty"`
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

type DeniedError struct {
	Request Request
}

func (e *DeniedError) Error() string {
	if e.Request.Reason == "" {
		return fmt.Sprintf("permission denied for %s %s", e.Request.Tool, e.Request.Input)
	}
	return fmt.Sprintf("permission denied for %s %s: %s", e.Request.Tool, e.Request.Input, e.Request.Reason)
}

type Policy struct {
	Mode                   Mode
	Approved               bool
	AllowWritesInPatchMode bool
	NetworkDefaultDeny     bool
	NetworkAllowlist       []string
}

func (p Policy) Check(_ context.Context, request Request) error {
	if isUntrustedPolicyOverride(request) {
		return &DeniedError{Request: Request{
			Tool:          strings.ToLower(strings.TrimSpace(request.Tool)),
			Input:         strings.TrimSpace(request.Input),
			Risk:          "policy_override",
			Reason:        "Untrusted content cannot change policy, approval rules, or secret handling.",
			Trust:         trust.LevelUntrusted,
			ContentSource: trust.NormalizeSource(request.ContentSource),
		}}
	}
	classified, required := Classify(request.Tool, request.Input, p.AllowWritesInPatchMode)
	if !required {
		return nil
	}
	if classified.Risk == "network" && p.networkAllowed(classified.Input) {
		return nil
	}
	if p.Approved {
		return nil
	}
	if p.Mode != ModePrompt && !(classified.Risk == "network" && p.NetworkDefaultDeny) {
		return nil
	}
	return &RequiredError{Request: classified}
}

func isUntrustedPolicyOverride(request Request) bool {
	contentSource := trust.NormalizeSource(request.ContentSource)
	level := trust.NormalizeLevel(request.Trust)
	isUntrusted := level == trust.LevelUntrusted || trust.LevelForSource(contentSource) == trust.LevelUntrusted
	if !isUntrusted {
		return false
	}
	input := " " + strings.ToLower(strings.Join(strings.Fields(request.Input), " ")) + " "
	patterns := []string{
		" always allow ",
		" always deny ",
		" always ask ",
		" auto-approve ",
		" auto approve ",
		" approve without asking ",
		" bypass approval ",
		" skip approval ",
		" modify policy ",
		" change policy ",
		" update policy ",
		" approval rule ",
		" permission rule ",
		" print api_key ",
		" print api key ",
		" leak secret ",
		" reveal secret ",
		" exfiltrate secret ",
	}
	for _, pattern := range patterns {
		if strings.Contains(input, pattern) {
			return true
		}
	}
	return false
}

func (p Policy) networkAllowed(command string) bool {
	hosts := networkHosts(command)
	if len(hosts) == 0 {
		return false
	}
	allowlist := normalizedDomains(p.NetworkAllowlist)
	if len(allowlist) == 0 {
		return false
	}
	for _, host := range hosts {
		if !domainAllowed(host, allowlist) {
			return false
		}
	}
	return true
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
		if reason := networkShellReason(input); reason != "" {
			return Request{Tool: tool, Input: input, Risk: "network", Reason: reason}, true
		}
	case "mcp":
		return Request{Tool: tool, Input: input, Risk: "external", Reason: "This step calls an external MCP tool."}, true
	case "hook":
		return Request{Tool: tool, Input: input, Risk: "hook_side_effect", Reason: "This step runs a hook with possible side effects."}, true
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

func networkShellReason(command string) string {
	normalized := " " + strings.ToLower(strings.Join(strings.Fields(command), " ")) + " "
	patterns := []string{
		" curl ",
		" wget ",
		" nc ",
		" ncat ",
		" telnet ",
		" ssh ",
		" scp ",
		" rsync ",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return "Command may access the network."
		}
	}
	if strings.Contains(normalized, "http://") || strings.Contains(normalized, "https://") {
		return "Command references a network URL."
	}
	return ""
}

func networkHosts(command string) []string {
	fields := strings.Fields(command)
	var hosts []string
	for index, field := range fields {
		cleaned := strings.Trim(field, " \t\r\n\"'`<>()[]{}")
		lower := strings.ToLower(cleaned)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			if parsed, err := url.Parse(cleaned); err == nil && parsed.Hostname() != "" {
				hosts = append(hosts, strings.ToLower(parsed.Hostname()))
			}
			continue
		}
		if index > 0 && isNetworkCommand(fields[index-1]) && !strings.HasPrefix(cleaned, "-") {
			if host := hostFromCommandArgument(cleaned); host != "" {
				hosts = append(hosts, strings.ToLower(host))
			}
		}
	}
	return hosts
}

func isNetworkCommand(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "curl", "wget", "ssh", "scp", "rsync", "nc", "ncat", "telnet":
		return true
	default:
		return false
	}
}

func hostFromCommandArgument(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		return parsed.Hostname()
	}
	value = strings.TrimPrefix(value, "//")
	if at := strings.LastIndex(value, "@"); at >= 0 {
		value = value[at+1:]
	}
	if slash := strings.IndexAny(value, "/:"); slash >= 0 {
		value = value[:slash]
	}
	return strings.Trim(value, " \t\r\n\"'")
}

func normalizedDomains(values []string) []string {
	var domains []string
	for _, value := range values {
		value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
		if value != "" {
			domains = append(domains, value)
		}
	}
	return domains
}

func domainAllowed(host string, allowlist []string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), ".")
	for _, domain := range allowlist {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
