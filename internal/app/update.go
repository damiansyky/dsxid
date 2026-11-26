package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	releaseAPIURL   = "https://api.github.com/repos/damiansyky/dsxid/releases/latest"
	releasesPageURL = "https://github.com/damiansyky/dsxid/releases/"
	updateTimeout   = 5 * time.Second
)

// UpdateInfo is the result of the startup release check.
type UpdateInfo struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	URL       string `json:"url,omitempty"`
}

// CheckForUpdates asks GitHub for the latest published release. The frontend
// calls it once after startup; offline and rate-limited checks fail silently.
func (a *App) CheckForUpdates() UpdateInfo {
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	client := &http.Client{Timeout: updateTimeout}

	info, err := checkForUpdates(ctx, client, releaseAPIURL, Version)
	if err != nil {
		log.Printf("update check failed: %v", err)
		return UpdateInfo{Current: Version}
	}
	log.Printf("update check: current=%s latest=%s available=%t", info.Current, info.Latest, info.Available)
	return info
}

// checkForUpdates is split from the App method so tests can use an httptest
// server. The response is only used to suggest a link.
func checkForUpdates(ctx context.Context, client *http.Client, apiURL, current string) (UpdateInfo, error) {
	info := UpdateInfo{Current: current}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "DsxID/"+current)

	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("update check: HTTP %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	// A release object is small; the limit stops a bad response from eating memory.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return info, fmt.Errorf("update check: %w", err)
	}

	// releases/latest already excludes drafts and prereleases. Normalize the tag
	// so "v0.1.1" and "0.1.1" compare the same way.
	latest := normalizeVersion(release.TagName)
	if _, ok := parseVersion(latest); !ok {
		return info, fmt.Errorf("update check: invalid release tag %q", release.TagName)
	}
	info.Latest = latest
	if compareVersions(latest, current) <= 0 {
		return info, nil
	}

	info.Available = true
	info.URL = releasesPageURL
	// Only trust a release URL that still points at this repository. Anything
	// else falls back to the releases page shipped in the binary.
	if isReleaseURL(release.HTMLURL) {
		info.URL = release.HTMLURL
	}
	return info, nil
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	return strings.TrimPrefix(value, "V")
}

// parseVersion accepts 1, 1.2, or 1.2.3 with an optional v prefix and
// prerelease/build suffix. Anything else is ignored by the update check.
func parseVersion(value string) ([3]int, bool) {
	var out [3]int
	value = normalizeVersion(value)
	if i := strings.IndexAny(value, "-+"); i >= 0 {
		value = value[:i]
	}
	if value == "" {
		return out, false
	}

	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, part := range parts {
		if part == "" {
			return out, false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return out, false
		}
		out[i] = number
	}
	return out, true
}

// DsxID uses simple numeric tags, so a full semver dependency isn't worth it.
func compareVersions(a, b string) int {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0
	}
	for i := 0; i < 3; i++ {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}

// Only accept release URLs for this repository.
func isReleaseURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" {
		return false
	}
	return strings.HasPrefix(parsed.Path, "/damiansyky/dsxid/releases/")
}
