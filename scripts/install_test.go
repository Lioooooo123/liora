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
	for _, want := range []string{`go build`, `apps/cli`, `liora`, `.local/bin`} {
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
		`docs/release.md`,
		`docs/mvp-exit-benchmark.md`,
		`docs/v0.1-exit-audit.md`,
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
	for _, want := range []string{`tar -xzf`, `install.sh`, `README.md`, `docs/release.md`, `LIORA_INSTALL_DIR`, `-version`} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected release smoke script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestExitAuditUsesTempInstallAndStrictGitChecks(t *testing.T) {
	data, err := os.ReadFile("v0.1-exit-audit.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`AUDIT_TMP="$(mktemp -d)"`,
		`LIORA_INSTALL_DIR="$INSTALL_DIR"`,
		`"$INSTALL_DIR/liora" -version`,
		`git rev-parse --abbrev-ref HEAD`,
		`git rev-parse --abbrev-ref --symbolic-full-name '@{u}'`,
		`git rev-list --left-right --count "HEAD...$UPSTREAM"`,
		`v0.1 exit audit must run on main`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected exit audit script to contain %q, got:\n%s", want, content)
		}
	}
}
