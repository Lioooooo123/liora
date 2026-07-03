package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func verifyRemoteChecksum(ctx context.Context, client *http.Client, checksumURL string, archivePath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return fmt.Errorf("create checksum request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("download checksum: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read checksum: %w", err)
	}
	expected := strings.Fields(string(data))
	if len(expected) == 0 {
		return errors.New("checksum file is empty")
	}
	actual, err := fileSHA256(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected[0], actual) {
		return fmt.Errorf("checksum mismatch for %s", filepath.Base(archivePath))
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open checksum target: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash checksum target: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func installArchiveBinary(archivePath string, installPath string) error {
	if strings.TrimSpace(archivePath) == "" {
		return errors.New("release archive path is empty")
	}
	if strings.TrimSpace(installPath) == "" {
		return errors.New("install path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		return fmt.Errorf("create install directory: %w", err)
	}
	tempPath := filepath.Join(filepath.Dir(installPath), ".liora-update-"+filepath.Base(installPath))
	if err := extractLioraBinary(archivePath, tempPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Chmod(tempPath, 0o755); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("make updated binary executable: %w", err)
	}
	if err := os.Rename(tempPath, installPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace installed binary: %w", err)
	}
	return nil
}

func extractLioraBinary(archivePath string, destination string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open release archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("read release archive gzip: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read release archive tar: %w", err)
		}
		cleanName := path.Clean(header.Name)
		if header.Typeflag != tar.TypeReg || !strings.HasSuffix(cleanName, "/bin/"+updateExecutableName()) {
			continue
		}
		return writeArchiveEntry(destination, reader)
	}
	return fmt.Errorf("release archive missing bin/%s", updateExecutableName())
}

func writeArchiveEntry(destination string, reader io.Reader) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create updated binary: %w", err)
	}
	if _, err := io.Copy(output, reader); err != nil {
		_ = output.Close()
		return fmt.Errorf("extract updated binary: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close updated binary: %w", err)
	}
	return nil
}
