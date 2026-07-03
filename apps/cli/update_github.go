package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
)

func fetchGitHubWebReleaseMetadata(ctx context.Context, client *http.Client, metadataURL string) (releaseMetadata, error) {
	var release releaseMetadata
	releaseURL, err := githubWebLatestURL(metadataURL)
	if err != nil {
		return release, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return release, fmt.Errorf("create release page request: %w", err)
	}
	req.Header.Set("User-Agent", "liora-update")
	resp, err := client.Do(req)
	if err != nil {
		return release, fmt.Errorf("fetch release page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return release, fmt.Errorf("fetch release page: %s", resp.Status)
	}
	tag, owner, repo, err := parseGitHubReleaseTag(resp.Request.URL)
	if err != nil {
		return release, err
	}
	name := "liora_" + tag + "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	base := resp.Request.URL.Scheme + "://" + resp.Request.URL.Host + "/" + owner + "/" + repo + "/releases/download/" + url.PathEscape(tag) + "/"
	return releaseMetadata{
		TagName: tag,
		Assets: []releaseAsset{
			{Name: name, URL: base + name},
			{Name: name + ".sha256", URL: base + name + ".sha256"},
		},
	}, nil
}

func githubWebLatestURL(metadataURL string) (string, error) {
	parsed, err := url.Parse(metadataURL)
	if err != nil {
		return "", fmt.Errorf("parse update metadata URL: %w", err)
	}
	parts := splitURLPath(parsed.Path)
	if len(parts) >= 5 && parts[0] == "repos" && parts[3] == "releases" && parts[4] == "latest" {
		host := parsed.Host
		if host == "api.github.com" {
			host = "github.com"
		}
		return parsed.Scheme + "://" + host + "/" + parts[1] + "/" + parts[2] + "/releases/latest", nil
	}
	if len(parts) >= 4 && parts[2] == "releases" && parts[3] == "latest" {
		return parsed.Scheme + "://" + parsed.Host + "/" + parts[0] + "/" + parts[1] + "/releases/latest", nil
	}
	return "", fmt.Errorf("update metadata URL is not a GitHub latest release URL: %s", metadataURL)
}

func parseGitHubReleaseTag(releaseURL *url.URL) (string, string, string, error) {
	parts := splitURLPath(releaseURL.Path)
	if len(parts) < 5 || parts[2] != "releases" || parts[3] != "tag" || strings.TrimSpace(parts[4]) == "" {
		return "", "", "", fmt.Errorf("release page did not resolve to a tag URL: %s", releaseURL.String())
	}
	return parts[4], parts[0], parts[1], nil
}

func splitURLPath(pathValue string) []string {
	var parts []string
	for _, part := range strings.Split(pathValue, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
