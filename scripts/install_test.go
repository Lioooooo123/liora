package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestInstallScriptBuildsLioraBinary(t *testing.T) {
	data, err := os.ReadFile("install-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{`go build`, `cmd/coding-agent`, `liora`, `.local/bin`} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected install script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestPackageReleaseScriptBuildsInstallableArchive(t *testing.T) {
	data, err := os.ReadFile("package-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`go build`,
		`-ldflags`,
		`main.version`,
		`bin/liora`,
		`install.sh`,
		`README.md`,
		`docs/mvp-exit-benchmark.md`,
		`shasum -a 256`,
		`.tar.gz`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected package script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestReleaseSmokeScriptInstallsArchive(t *testing.T) {
	data, err := os.ReadFile("release-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{`tar -xzf`, `install.sh`, `LIORA_INSTALL_DIR`, `-version`} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected release smoke script to contain %q, got:\n%s", want, content)
		}
	}
}
