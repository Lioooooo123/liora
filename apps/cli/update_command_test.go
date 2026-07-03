package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIUpdateInstallsFromReleaseArchive(t *testing.T) {
	// Given
	archive := writeUpdateArchive(t, "v9.9.9")
	installDir := t.TempDir()
	cmd := exec.Command("go", "run", ".", "update", "--from", archive, "--install-dir", installDir)
	cmd.Env = cleanLLMEnv(t)

	// When
	output, err := cmd.CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("update command failed: %v\n%s", err, string(output))
	}
	installed := filepath.Join(installDir, "liora")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "updated-v9.9.9") {
		t.Fatalf("expected update to install archive binary, got:\n%s", string(data))
	}
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected installed binary to be executable, mode=%s", info.Mode())
	}
	rendered := string(output)
	for _, want := range []string{"current:", "installed:", installed} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected update output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestCLIUpdateCheckReadsLatestReleaseMetadata(t *testing.T) {
	// Given
	archive := writeUpdateArchive(t, "v9.9.10")
	checksum := sha256File(t, archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"tag_name":"v9.9.10",
				"assets":[
					{"name":"liora_v9.9.10_%s_%s.tar.gz","browser_download_url":"%s"},
					{"name":"liora_v9.9.10_%s_%s.tar.gz.sha256","browser_download_url":"%s"}
				]
			}`, runtimeGOOS(), runtimeGOARCH(), serverURL(r, "archive"), runtimeGOOS(), runtimeGOARCH(), serverURL(r, "checksum"))
		case "/archive":
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(data)
		case "/checksum":
			_, _ = fmt.Fprintf(w, "%s  liora_v9.9.10_%s_%s.tar.gz\n", checksum, runtimeGOOS(), runtimeGOARCH())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cmd := exec.Command("go", "run", ".", "update", "--check", "--metadata-url", server.URL+"/releases/latest")
	cmd.Env = cleanLLMEnv(t)

	// When
	output, err := cmd.CombinedOutput()

	// Then
	if err != nil {
		t.Fatalf("update check failed: %v\n%s", err, string(output))
	}
	rendered := string(output)
	for _, want := range []string{"current:", "latest: v9.9.10", "update_available:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected update check output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestUpdateDownloadsLatestReleaseAssetWithChecksum(t *testing.T) {
	// Given
	archive := writeUpdateArchive(t, "v9.9.11")
	checksum := sha256File(t, archive)
	server := httptest.NewServer(updateReleaseHandler(t, archive, checksum, "v9.9.11"))
	defer server.Close()
	installDir := t.TempDir()
	var output strings.Builder

	// When
	err := runUpdate(t.Context(), updateConfig{
		currentVersion: "v0.0.1",
		installDir:     installDir,
		metadataURL:    server.URL + "/releases/latest",
		stdout:         &output,
		client:         server.Client(),
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(installDir, "liora")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "updated-v9.9.11") {
		t.Fatalf("expected downloaded release binary to be installed, got:\n%s", string(data))
	}
	rendered := output.String()
	for _, want := range []string{"latest: v9.9.11", "update_available: true", "installed: " + installed} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected update output to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestUpdateFallsBackToGitHubReleasePageWhenAPIIsRateLimited(t *testing.T) {
	// Given
	archive := writeUpdateArchive(t, "v9.9.12")
	checksum := sha256File(t, archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Lioooooo123/liora/releases/latest":
			http.Error(w, "rate limited", http.StatusForbidden)
		case "/Lioooooo123/liora/releases/latest":
			http.Redirect(w, r, "/Lioooooo123/liora/releases/tag/v9.9.12", http.StatusFound)
		case "/Lioooooo123/liora/releases/tag/v9.9.12":
			_, _ = w.Write([]byte("<html>latest</html>"))
		case "/Lioooooo123/liora/releases/download/v9.9.12/liora_v9.9.12_" + runtimeGOOS() + "_" + runtimeGOARCH() + ".tar.gz":
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(data)
		case "/Lioooooo123/liora/releases/download/v9.9.12/liora_v9.9.12_" + runtimeGOOS() + "_" + runtimeGOARCH() + ".tar.gz.sha256":
			_, _ = fmt.Fprintf(w, "%s  archive\n", checksum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	installDir := t.TempDir()
	var output strings.Builder

	// When
	err := runUpdate(t.Context(), updateConfig{
		currentVersion: "v0.0.1",
		installDir:     installDir,
		metadataURL:    server.URL + "/repos/Lioooooo123/liora/releases/latest",
		stdout:         &output,
		client:         server.Client(),
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(installDir, "liora")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "updated-v9.9.12") {
		t.Fatalf("expected fallback release binary to be installed, got:\n%s", string(data))
	}
	if !strings.Contains(output.String(), "latest: v9.9.12") {
		t.Fatalf("expected fallback output to report latest tag, got:\n%s", output.String())
	}
}

func writeUpdateArchive(t *testing.T, version string) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "liora_"+version+"_"+runtimeGOOS()+"_"+runtimeGOARCH()+".tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	content := []byte("#!/bin/sh\necho updated-" + version + "\n")
	header := &tar.Header{
		Name: "liora_" + version + "_" + runtimeGOOS() + "_" + runtimeGOARCH() + "/bin/liora",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func updateReleaseHandler(t *testing.T, archive string, checksum string, version string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"tag_name":%q,
				"assets":[
					{"name":"liora_%s_%s_%s.tar.gz","browser_download_url":"%s"},
					{"name":"liora_%s_%s_%s.tar.gz.sha256","browser_download_url":"%s"}
				]
			}`, version, version, runtimeGOOS(), runtimeGOARCH(), serverURL(r, "archive"), version, runtimeGOOS(), runtimeGOARCH(), serverURL(r, "checksum"))
		case "/archive":
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(data)
		case "/checksum":
			_, _ = fmt.Fprintf(w, "%s  liora_%s_%s_%s.tar.gz\n", checksum, version, runtimeGOOS(), runtimeGOARCH())
		default:
			http.NotFound(w, r)
		}
	})
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func runtimeGOOS() string {
	return runtime.GOOS
}

func runtimeGOARCH() string {
	return runtime.GOARCH
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + "/" + path
}
