package trust

import "strings"

const (
	LevelTrusted   = "trusted"
	LevelUntrusted = "untrusted"
)

const (
	SourceSystem          = "system"
	SourceRepoFile        = "repo_file"
	SourceMCPOutput       = "mcp_output"
	SourceToolOutput      = "tool_output"
	SourceHookOutput      = "hook_output"
	SourceArtifact        = "artifact"
	SourceTranscript      = "transcript"
	SourceMemoryCandidate = "memory_candidate"
)

func NormalizeSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	source = strings.ReplaceAll(source, "-", "_")
	source = strings.ReplaceAll(source, " ", "_")
	return source
}

func NormalizeLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

func IsTrustedSource(source string) bool {
	return NormalizeSource(source) == SourceSystem
}

func IsUntrustedSource(source string) bool {
	switch NormalizeSource(source) {
	case SourceRepoFile, SourceMCPOutput, SourceToolOutput, SourceHookOutput, SourceArtifact, SourceTranscript, SourceMemoryCandidate:
		return true
	default:
		return false
	}
}

func LevelForSource(source string) string {
	source = NormalizeSource(source)
	if IsTrustedSource(source) {
		return LevelTrusted
	}
	if IsUntrustedSource(source) || source != "" {
		return LevelUntrusted
	}
	return ""
}
