package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseSupplyChainAuditAcceptsValidSidecars(t *testing.T) {
	archive := writeSupplyChainFixture(t)

	output, err := runSupplyChainAudit(archive)
	if err != nil {
		t.Fatalf("expected valid supply-chain sidecars to pass: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "release supply-chain audit ok") {
		t.Fatalf("expected success output, got:\n%s", output)
	}
}

func TestReleaseSupplyChainAuditRejectsInvalidSidecars(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, archive string)
		want   string
	}{
		{
			name: "missing checksum",
			mutate: func(t *testing.T, archive string) {
				t.Helper()
				if err := os.Remove(archive + ".sha256"); err != nil {
					t.Fatal(err)
				}
			},
			want: "checksum not found",
		},
		{
			name: "mismatched checksum",
			mutate: func(t *testing.T, archive string) {
				t.Helper()
				if err := os.WriteFile(archive+".sha256", []byte(strings.Repeat("0", 64)+"  "+filepath.Base(archive)+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "computed checksum did NOT match",
		},
		{
			name: "malformed provenance",
			mutate: func(t *testing.T, archive string) {
				t.Helper()
				if err := os.WriteFile(archive+".provenance.json", []byte("{"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "malformed provenance",
		},
		{
			name: "empty dependency inventory",
			mutate: func(t *testing.T, archive string) {
				t.Helper()
				writeJSON(t, archive+".sbom.json", map[string]any{
					"package_name": "liora_test_darwin_arm64",
					"version":      "vtest",
					"git_commit":   "abc123",
					"go_modules":   []any{},
				})
			},
			want: "dependency inventory is empty",
		},
		{
			name: "unsafe MCP server",
			mutate: func(t *testing.T, archive string) {
				t.Helper()
				writeJSON(t, archive+".manifest-review.json", map[string]any{
					"package_name": "liora_test_darwin_arm64",
					"version":      "vtest",
					"git_commit":   "abc123",
					"verdict":      "pass",
					"mcp_servers": []any{
						map[string]any{"name": "unknown", "network_enabled": true},
					},
					"hooks": []any{},
				})
			},
			want: "unsafe MCP server requires approval",
		},
		{
			name: "unsafe hook command",
			mutate: func(t *testing.T, archive string) {
				t.Helper()
				writeJSON(t, archive+".manifest-review.json", map[string]any{
					"package_name": "liora_test_darwin_arm64",
					"version":      "vtest",
					"git_commit":   "abc123",
					"verdict":      "pass",
					"mcp_servers":  []any{},
					"hooks": []any{
						map[string]any{"name": "postinstall", "command": "/tmp/liora-hook"},
					},
				})
			},
			want: "unsafe hook command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := writeSupplyChainFixture(t)
			tt.mutate(t, archive)

			output, err := runSupplyChainAudit(archive)
			if err == nil {
				t.Fatalf("expected supply-chain audit to fail, got success:\n%s", output)
			}
			if !strings.Contains(string(output), tt.want) {
				t.Fatalf("expected output to contain %q, got:\n%s", tt.want, output)
			}
		})
	}
}

func writeSupplyChainFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	archive := filepath.Join(root, "liora_test_darwin_arm64.tar.gz")
	archiveBytes := []byte("fake release archive")
	if err := os.WriteFile(archive, archiveBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sumBytes := sha256.Sum256(archiveBytes)
	sum := hex.EncodeToString(sumBytes[:])
	if err := os.WriteFile(archive+".sha256", []byte(sum+"  "+filepath.Base(archive)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, archive+".provenance.json", map[string]any{
		"package_name": "liora_test_darwin_arm64",
		"version":      "vtest",
		"git_commit":   "abc123",
		"archive":      filepath.Base(archive),
		"sha256":       sum,
	})
	writeJSON(t, archive+".sbom.json", map[string]any{
		"package_name": "liora_test_darwin_arm64",
		"version":      "vtest",
		"git_commit":   "abc123",
		"go_modules": []any{
			map[string]any{"path": "github.com/Lioooooo123/liora", "main": true},
		},
	})
	writeJSON(t, archive+".manifest-review.json", map[string]any{
		"package_name": "liora_test_darwin_arm64",
		"version":      "vtest",
		"git_commit":   "abc123",
		"verdict":      "pass",
		"mcp_servers":  []any{},
		"hooks":        []any{},
	})
	return archive
}

func runSupplyChainAudit(archive string) ([]byte, error) {
	cmd := exec.Command("./release-supply-chain-audit.sh", archive)
	cmd.Dir = "."
	return cmd.CombinedOutput()
}

func writeJSON(t *testing.T, path string, doc any) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
