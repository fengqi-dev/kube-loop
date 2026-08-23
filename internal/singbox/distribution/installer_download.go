package distribution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (i *Installer) downloadToCores(ctx context.Context) (string, error) {
	goos, goarch := i.platform()
	asset, ok := releaseAssets[goos+"/"+goarch]
	if i.Asset != nil {
		asset, ok = *i.Asset, true
	}
	if !ok {
		return "", fmt.Errorf("sing-box %s is not available for %s/%s", Version, goos, goarch)
	}
	baseDir, err := i.baseDir()
	if err != nil {
		return "", err
	}
	binaryName := "sing-box-" + Version
	if goos == windowsGOOS {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(baseDir, "cores", binaryName)
	if path, validateErr := validateBinary(binaryPath); validateErr == nil {
		return path, nil
	}
	//nolint:gosec // The core executable directory must be traversable by the helper service.
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		return "", fmt.Errorf("create sing-box core directory: %w", err)
	}

	client := i.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	url := downloadBaseURL + "/" + asset.Name
	if i.DownloadURL != nil {
		url = i.DownloadURL(asset)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create sing-box download request: %w", err)
	}
	request.Header.Set("User-Agent", "kubeloop/0.1")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download sing-box: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download sing-box: unexpected HTTP status %s", response.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, 128<<20))
	if err != nil {
		return "", fmt.Errorf("read sing-box download: %w", err)
	}
	if err := verifySHA256(archive, asset.SHA256); err != nil {
		return "", err
	}
	files, err := extractReleaseFiles(asset.Name, archive)
	if err != nil {
		return "", err
	}
	executable, ok := files[singBoxBinary]
	if !ok {
		if executable, ok = files[singBoxBinaryWin]; !ok {
			return "", errors.New("sing-box archive does not contain sing-box binary")
		}
	}
	if err := writeExecutable(binaryPath, executable); err != nil {
		return "", err
	}
	if goos == windowsGOOS {
		for _, sidecar := range []string{wintunDLL, cronetDLL} {
			if payload, ok := files[sidecar]; ok {
				//nolint:gosec // Windows runtime sidecars are public binaries and contain no secrets.
				if err := os.WriteFile(filepath.Join(filepath.Dir(binaryPath), sidecar), payload, 0o644); err != nil {
					return "", fmt.Errorf("install %s: %w", sidecar, err)
				}
			}
		}
	}
	return validateBinary(binaryPath)
}
