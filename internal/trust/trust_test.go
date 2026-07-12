package trust

import "testing"

func TestNormalizeSource(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"system", "system"},
		{"  System  ", "system"},
		{"MCP-Output", "mcp_output"},
		{"tool output", "tool_output"},
		{"Repo File", "repo_file"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeSource(tc.in); got != tc.want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeLevel(t *testing.T) {
	if got := NormalizeLevel("  Trusted "); got != LevelTrusted {
		t.Errorf("NormalizeLevel = %q, want %q", got, LevelTrusted)
	}
}

func TestIsTrustedSource(t *testing.T) {
	if !IsTrustedSource("system") {
		t.Error("system should be trusted")
	}
	if !IsTrustedSource(" SYSTEM ") {
		t.Error("normalized system should be trusted")
	}
	for _, source := range []string{SourceRepoFile, SourceMCPOutput, SourceToolOutput, SourceHookOutput, SourceArtifact, SourceTranscript, SourceMemoryCandidate, "unknown", ""} {
		if IsTrustedSource(source) {
			t.Errorf("%q should not be trusted", source)
		}
	}
}

func TestIsUntrustedSource(t *testing.T) {
	for _, source := range []string{SourceRepoFile, SourceMCPOutput, SourceToolOutput, SourceHookOutput, SourceArtifact, SourceTranscript, SourceMemoryCandidate} {
		if !IsUntrustedSource(source) {
			t.Errorf("%q should be untrusted", source)
		}
	}
	if IsUntrustedSource(SourceSystem) {
		t.Error("system should not be untrusted")
	}
	if IsUntrustedSource("unknown") {
		t.Error("unknown source is not in the untrusted set")
	}
	if !IsUntrustedSource("MCP-Output") {
		t.Error("source should be normalized before matching")
	}
}

func TestLevelForSource(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{SourceSystem, LevelTrusted},
		{" System ", LevelTrusted},
		{SourceRepoFile, LevelUntrusted},
		{SourceMCPOutput, LevelUntrusted},
		{"some-future-source", LevelUntrusted},
		{"", ""},
	}
	for _, tc := range cases {
		if got := LevelForSource(tc.source); got != tc.want {
			t.Errorf("LevelForSource(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}
