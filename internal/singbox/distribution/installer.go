package distribution

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const downloadBaseURL = ProjectURL + "/releases/download/" + Version

type releaseAsset struct {
	Name   string
	SHA256 string
}

var releaseAssets = map[string]releaseAsset{
	"darwin/amd64": {
		Name:   "sing-box-1.13.16-darwin-amd64.tar.gz",
		SHA256: "2bfad58d034e280c773e194be03649555e5a7040c48b559dd0898ad293fe793d",
	},
	"darwin/arm64": {
		Name:   "sing-box-1.13.16-darwin-arm64.tar.gz",
		SHA256: "32fa21fd75ad62d86a2dcb7e0be77359c35e12798cdbb6a0e30654ef487d90d6",
	},
	"linux/amd64": {
		Name:   "sing-box-1.13.16-linux-amd64.tar.gz",
		SHA256: "e37c312859dfa84cba148f41072ff6369f08361ae91d622dc1fd3aab49611a8d",
	},
	"linux/arm64": {
		Name:   "sing-box-1.13.16-linux-arm64.tar.gz",
		SHA256: "d587fb00bdc3c044227f35d15d154f271bc75108475091eda2542e4b82bb2949",
	},
	"windows/amd64": {
		Name:   "sing-box-1.13.16-windows-amd64.zip",
		SHA256: "6cbf90ec4ee87122ffce09b73928fb31e763bc1c75a119f79c61d24734c78807",
	},
	"windows/arm64": {
		Name:   "sing-box-1.13.16-windows-arm64.zip",
		SHA256: "8412e9751a776a1cd5138fde8a6b60784af91b0fe596cba1b6efcd05144ef511",
	},
}

type Installer struct {
	HTTPClient      *http.Client
	BaseDir         string
	BundledPath     string
	GOOS            string
	GOARCH          string
	Asset           *releaseAsset
	DownloadURL     func(releaseAsset) string
	DisableOverride bool
	// DisableDownload skips downloading/copying into BaseDir/cores and only
	// returns an already-present bundled or override binary.
	DisableDownload bool
}

func (i *Installer) Ensure(ctx context.Context) (string, error) {
	if !i.DisableOverride {
		if override := os.Getenv("KUBELOOP_SINGBOX_PATH"); override != "" {
			return validateBinary(override)
		}
	}
	var missing []string
	for _, candidate := range i.bundledCandidates() {
		path, err := validateBinary(candidate)
		if err == nil {
			return path, nil
		}
		missing = append(missing, candidate)
	}
	if i.DisableDownload {
		if len(missing) == 0 {
			return "", fmt.Errorf(
				"bundled sing-box %s not found; reinstall the KubeLoop package",
				Version,
			)
		}
		return "", fmt.Errorf(
			"bundled sing-box %s not found (tried %s); reinstall the KubeLoop package",
			Version,
			strings.Join(missing, ", "),
		)
	}
	return i.downloadToCores(ctx)
}

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
	if goos == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(baseDir, "cores", binaryName)
	if path, validateErr := validateBinary(binaryPath); validateErr == nil {
		return path, nil
	}
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
	defer response.Body.Close()
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
	executable, ok := files["sing-box"]
	if !ok {
		if executable, ok = files["sing-box.exe"]; !ok {
			return "", errors.New("sing-box archive does not contain sing-box binary")
		}
	}
	if err := writeExecutable(binaryPath, executable); err != nil {
		return "", err
	}
	if goos == "windows" {
		for _, sidecar := range []string{"wintun.dll", "libcronet.dll"} {
			if payload, ok := files[sidecar]; ok {
				if err := os.WriteFile(filepath.Join(filepath.Dir(binaryPath), sidecar), payload, 0o644); err != nil {
					return "", fmt.Errorf("install %s: %w", sidecar, err)
				}
			}
		}
	}
	return validateBinary(binaryPath)
}

func writeExecutable(binaryPath string, executable []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(binaryPath), ".sing-box-*")
	if err != nil {
		return fmt.Errorf("create temporary sing-box binary: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if runtime.GOOS != "windows" {
		if err := temp.Chmod(0o755); err != nil {
			temp.Close()
			return fmt.Errorf("set sing-box permissions: %w", err)
		}
	}
	if _, err := temp.Write(executable); err != nil {
		temp.Close()
		return fmt.Errorf("write sing-box binary: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync sing-box binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close sing-box binary: %w", err)
	}
	if err := os.Rename(tempPath, binaryPath); err != nil {
		return fmt.Errorf("install sing-box binary: %w", err)
	}
	return nil
}

func (i *Installer) platform() (string, string) {
	goos, goarch := i.GOOS, i.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (i *Installer) baseDir() (string, error) {
	if i.BaseDir != "" {
		return i.BaseDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find user home directory: %w", err)
	}
	return filepath.Join(home, ".kubeloop"), nil
}

func (i *Installer) bundledCandidates() []string {
	var candidates []string
	if i.BundledPath != "" {
		candidates = append(candidates, i.BundledPath)
	}
	goos, _ := i.platform()
	name := "sing-box"
	if goos == "windows" {
		name = "sing-box.exe"
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, name),              // next to helper service / app
			filepath.Join(dir, "..", name),        // resources/../sing-box
			filepath.Join(dir, "Resources", name), // macOS app Resources
			filepath.Join(dir, "..", "Resources", name),
		)
	}
	return candidates
}

func validateBinary(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("find sing-box binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("sing-box binary is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("sing-box binary is not executable")
	}
	return filepath.Clean(path), nil
}

func verifySHA256(content []byte, expected string) error {
	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("sing-box SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func extractReleaseFiles(name string, content []byte) (map[string][]byte, error) {
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return extractFromTarGz(content)
	case strings.HasSuffix(name, ".zip"):
		return extractFromZip(content)
	default:
		return nil, fmt.Errorf("unsupported sing-box archive %q", name)
	}
}

func extractFromZip(content []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("open sing-box zip archive: %w", err)
	}
	files := make(map[string][]byte)
	for _, file := range reader.File {
		if !file.FileInfo().Mode().IsRegular() {
			continue
		}
		base := filepath.Base(file.Name)
		switch strings.ToLower(base) {
		case "sing-box.exe", "wintun.dll", "libcronet.dll", "license":
		default:
			continue
		}
		opened, openErr := file.Open()
		if openErr != nil {
			return nil, fmt.Errorf("open %s: %w", base, openErr)
		}
		value, readErr := io.ReadAll(io.LimitReader(opened, 128<<20))
		opened.Close()
		if readErr != nil {
			return nil, fmt.Errorf("extract %s: %w", base, readErr)
		}
		files[strings.ToLower(base)] = value
	}
	if _, ok := files["sing-box.exe"]; !ok {
		return nil, errors.New("sing-box zip archive does not contain an executable")
	}
	return files, nil
}

func extractFromTarGz(content []byte) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("open sing-box gzip archive: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read sing-box tar archive: %w", nextErr)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != "sing-box" {
			continue
		}
		value, readErr := io.ReadAll(io.LimitReader(tarReader, 128<<20))
		if readErr != nil {
			return nil, fmt.Errorf("extract sing-box binary: %w", readErr)
		}
		files["sing-box"] = value
		return files, nil
	}
	return nil, errors.New("sing-box tar archive does not contain sing-box binary")
}
