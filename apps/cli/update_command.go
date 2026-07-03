package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultUpdateMetadataURL = "https://api.github.com/repos/Lioooooo123/liora/releases/latest"

type updateConfig struct {
	currentVersion string
	archivePath    string
	installDir     string
	metadataURL    string
	checkOnly      bool
	stdout         io.Writer
	client         *http.Client
}

type releaseMetadata struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func handleUpdateCommand(ctx context.Context, args []string, currentVersion string, stdout io.Writer, stderr io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "update" {
		return false, nil
	}
	flags := flag.NewFlagSet("liora update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	archivePath := flags.String("from", "", "install from a local Liora release tarball")
	installDir := flags.String("install-dir", "", "directory to install liora into; defaults to the current executable directory")
	checkOnly := flags.Bool("check", false, "check latest release metadata without installing")
	metadataURL := flags.String("metadata-url", getenvAny("LIORA_UPDATE_METADATA_URL", defaultUpdateMetadataURL), "release metadata URL")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	config := updateConfig{
		currentVersion: currentVersion,
		archivePath:    strings.TrimSpace(*archivePath),
		installDir:     strings.TrimSpace(*installDir),
		metadataURL:    strings.TrimSpace(*metadataURL),
		checkOnly:      *checkOnly,
		stdout:         stdout,
		client:         &http.Client{Timeout: 2 * time.Minute},
	}
	return true, runUpdate(ctx, config)
}

func runUpdate(ctx context.Context, config updateConfig) error {
	installPath, err := resolveUpdateInstallPath(config.installDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(config.stdout, "current: %s\n", config.currentVersion)
	if config.archivePath != "" {
		if config.checkOnly {
			fmt.Fprintf(config.stdout, "source: %s\n", config.archivePath)
			fmt.Fprintln(config.stdout, "update_available: unknown")
			return nil
		}
		if err := installArchiveBinary(config.archivePath, installPath); err != nil {
			return err
		}
		fmt.Fprintf(config.stdout, "installed: %s\n", installPath)
		return nil
	}
	release, err := fetchReleaseMetadata(ctx, config.client, config.metadataURL)
	if err != nil {
		return err
	}
	fmt.Fprintf(config.stdout, "latest: %s\n", release.TagName)
	fmt.Fprintf(config.stdout, "update_available: %t\n", strings.TrimSpace(config.currentVersion) != strings.TrimSpace(release.TagName))
	if config.checkOnly {
		return nil
	}
	archiveAsset, checksumAsset, err := selectPlatformReleaseAssets(release)
	if err != nil {
		return err
	}
	workDir, err := os.MkdirTemp("", "liora-update-*")
	if err != nil {
		return fmt.Errorf("create update workspace: %w", err)
	}
	defer os.RemoveAll(workDir)
	downloadedArchive := filepath.Join(workDir, archiveAsset.Name)
	if err := downloadFile(ctx, config.client, archiveAsset.URL, downloadedArchive); err != nil {
		return err
	}
	if checksumAsset.URL != "" {
		if err := verifyRemoteChecksum(ctx, config.client, checksumAsset.URL, downloadedArchive); err != nil {
			return err
		}
	}
	if err := installArchiveBinary(downloadedArchive, installPath); err != nil {
		return err
	}
	fmt.Fprintf(config.stdout, "installed: %s\n", installPath)
	return nil
}

func resolveUpdateInstallPath(installDir string) (string, error) {
	if installDir != "" {
		return filepath.Join(installDir, updateExecutableName()), nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	return executable, nil
}

func updateExecutableName() string {
	if runtime.GOOS == "windows" {
		return "liora.exe"
	}
	return "liora"
}

func fetchReleaseMetadata(ctx context.Context, client *http.Client, metadataURL string) (releaseMetadata, error) {
	var release releaseMetadata
	if strings.TrimSpace(metadataURL) == "" {
		return release, errors.New("update metadata URL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return release, fmt.Errorf("create update metadata request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "liora-update")
	resp, err := client.Do(req)
	if err != nil {
		return release, fmt.Errorf("fetch update metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return release, fmt.Errorf("fetch update metadata: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return release, fmt.Errorf("decode update metadata: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return release, errors.New("update metadata missing tag_name")
	}
	return release, nil
}

func selectPlatformReleaseAssets(release releaseMetadata) (releaseAsset, releaseAsset, error) {
	suffix := "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	var archive releaseAsset
	var checksum releaseAsset
	for _, asset := range release.Assets {
		switch {
		case strings.HasSuffix(asset.Name, suffix):
			archive = asset
		case archive.Name != "" && asset.Name == archive.Name+".sha256":
			checksum = asset
		}
	}
	if archive.Name == "" {
		return archive, checksum, fmt.Errorf("no release asset for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if checksum.Name == "" {
		for _, asset := range release.Assets {
			if asset.Name == archive.Name+".sha256" {
				checksum = asset
				break
			}
		}
	}
	return archive, checksum, nil
}

func downloadFile(ctx context.Context, client *http.Client, url string, destination string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("download URL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "liora-update")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download update asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("download update asset %s: %s", url, resp.Status)
	}
	file, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("create downloaded asset: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("write downloaded asset: %w", err)
	}
	return nil
}
