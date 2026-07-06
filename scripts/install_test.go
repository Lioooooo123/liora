package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestLocalInstallSmokeScriptRunsDoctorAndWorkspaceSmoke(t *testing.T) {
	data, err := os.ReadFile("local-install-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`install-local.sh`,
		`HOME="$HOME_DIR"`,
		`LIORA_INSTALL_DIR="$INSTALL_DIR"`,
		`"$INSTALL_DIR/liora" -doctor`,
		`arbitrary-workspace`,
		`workspace-smoke.txt`,
		`-prompt 'list .'`,
		`local install smoke ok`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected local install smoke script to contain %q, got:\n%s", want, content)
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
		`docs/*.md`,
		`shasum -a 256`,
		`provenance.json`,
		`sbom.json`,
		`manifest-review.json`,
		`.tar.gz`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected package script to contain %q, got:\n%s", want, content)
		}
	}
}

func TestNextReleaseVersionScriptIncrementsLatestSemverTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	script, err := filepath.Abs("next-release-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := tempGitRepoDir(t)
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	runGit(t, repo, "tag", "v0.1.1")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-am", "change")

	output := runCommand(t, repo, script)
	if got := strings.TrimSpace(output); got != "v0.1.2" {
		t.Fatalf("expected next version v0.1.2, got %q", got)
	}
}

func TestNextReleaseVersionScriptReturnsHeadTagForRerun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	script, err := filepath.Abs("next-release-version.sh")
	if err != nil {
		t.Fatal(err)
	}
	repo := tempGitRepoDir(t)
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	runGit(t, repo, "tag", "v0.2.0")

	output := runCommand(t, repo, script)
	if got := strings.TrimSpace(output); got != "v0.2.0" {
		t.Fatalf("expected existing HEAD tag v0.2.0, got %q", got)
	}
}

func TestMainReleaseWorkflowPublishesVersionedUpdateRelease(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`branches:`,
		`- main`,
		`permissions:`,
		`contents: write`,
		`runs-on: macos-14`,
		`fetch-depth: 0`,
		`./scripts/next-release-version.sh`,
		`LIORA_VERSION="${{ steps.version.outputs.version }}" GOOS=darwin GOARCH=arm64 ./scripts/package-release.sh`,
		`./scripts/release-smoke.sh "${{ steps.version.outputs.archive }}"`,
		`git tag -a "$VERSION"`,
		`gh release create "$VERSION"`,
		`--latest`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected release workflow to contain %q, got:\n%s", want, content)
		}
	}
}

func TestReleaseSmokeScriptInstallsArchive(t *testing.T) {
	data, err := os.ReadFile("release-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{`release-supply-chain-audit.sh`, `tar -xzf`, `install.sh`, `README.md`, `docs/00-index.md`, `docs/06-release-packaging.md`, `docs/10-16-personality-agent-prd.md`, `docs/11-16-personality-agent-persona-spec.md`, `docs/12-16人格日记本.md`, `LIORA_INSTALL_DIR`, `-version`, `-doctor`, `arbitrary-workspace`, `workspace-smoke.txt`, `-prompt 'list .'`} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected release smoke script to contain %q, got:\n%s", want, content)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitArgs := append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)
	runCommand(t, dir, "git", gitArgs...)
}

func tempGitRepoDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "liora-release-version-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func runCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(output))
	}
	return string(output)
}

func TestNPMLazySmokeScriptExercisesGitHubPackageLazyBuild(t *testing.T) {
	data, err := os.ReadFile("npm-lazy-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		`scripts/npm/liora.cjs`,
		`scripts/npm`,
		`rm -rf "$PACKAGE_DIR/bin"`,
		`node "$PACKAGE_DIR/scripts/npm/liora.cjs" -doctor`,
		`"$PACKAGE_DIR/bin/liora"`,
		`arbitrary-workspace`,
		`workspace-smoke.txt`,
		`-prompt 'list .'`,
		`npm lazy smoke ok`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected npm lazy smoke script to contain %q, got:\n%s", want, content)
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
