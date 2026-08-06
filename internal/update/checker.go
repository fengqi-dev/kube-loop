package update

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	version "github.com/hashicorp/go-version"
)

const (
	latestReleaseURL = "https://api.github.com/repos/fengqi-dev/kube-loop/releases/latest"
	releasesPageURL  = "https://github.com/fengqi-dev/kube-loop/releases"
)

type Info struct {
	CurrentVersion string    `json:"currentVersion"`
	LatestVersion  string    `json:"latestVersion,omitempty"`
	Available      bool      `json:"available"`
	URL            string    `json:"url"`
	PublishedAt    time.Time `json:"publishedAt,omitzero"`
	CheckedAt      time.Time `json:"checkedAt"`
	Error          string    `json:"error,omitempty"`
}

type Checker struct {
	CurrentVersion string
	HTTPClient     *http.Client
	LatestURL      string
}

func (c *Checker) Check(ctx context.Context) (Info, error) {
	current := cmp.Or(c.CurrentVersion, "dev")
	info := Info{
		CurrentVersion: current,
		URL:            releasesPageURL,
		CheckedAt:      time.Now(),
	}
	url := cmp.Or(c.LatestURL, latestReleaseURL)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return info, fmt.Errorf("create update request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "kube-loop/"+current)

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return info, fmt.Errorf("check for updates: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return info, errors.New("no published KubeLoop release was found")
	}
	if response.StatusCode != http.StatusOK {
		return info, fmt.Errorf("check for updates: GitHub returned %s", response.Status)
	}
	var release struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&release); err != nil {
		return info, fmt.Errorf("decode latest release: %w", err)
	}
	if release.TagName == "" {
		return info, errors.New("latest GitHub release has no version tag")
	}
	info.LatestVersion = release.TagName
	info.PublishedAt = release.PublishedAt
	if strings.HasPrefix(release.HTMLURL, "https://github.com/fengqi-dev/kube-loop/") {
		info.URL = release.HTMLURL
	}
	if _, err := parseVersion(current); err == nil {
		info.Available = compareVersions(release.TagName, current) > 0
	}
	return info, nil
}

func compareVersions(left, right string) int {
	leftVersion, leftErr := parseVersion(left)
	rightVersion, rightErr := parseVersion(right)
	if leftErr != nil || rightErr != nil {
		return 0
	}
	return leftVersion.Compare(rightVersion)
}

func parseVersion(value string) (*version.Version, error) {
	return version.NewVersion(strings.TrimSpace(value))
}
