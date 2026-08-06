package distribution

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// BundleRelease downloads the pinned sing-box archive for goos/goarch, verifies
// its SHA-256, and writes stable filenames into outDir:
//
//	sing-box[.exe], wintun.dll (Windows), LICENSE.sing-box.txt
func BundleRelease(goos, goarch, outDir string) error {
	asset, ok := releaseAssets[goos+"/"+goarch]
	if !ok {
		return fmt.Errorf("sing-box %s is not available for %s/%s", Version, goos, goarch)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create sing-box output directory: %w", err)
	}

	client := &http.Client{Timeout: 3 * time.Minute}
	url := downloadBaseURL + "/" + asset.Name
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create sing-box download request: %w", err)
	}
	request.Header.Set("User-Agent", "kubeloop-build/0.1")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download sing-box: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download sing-box: unexpected HTTP status %s", response.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, 128<<20))
	if err != nil {
		return fmt.Errorf("read sing-box download: %w", err)
	}
	if err := verifySHA256(archive, asset.SHA256); err != nil {
		return err
	}
	files, err := extractReleaseFiles(asset.Name, archive)
	if err != nil {
		return err
	}

	binaryName := "sing-box"
	executable, ok := files["sing-box"]
	if !ok {
		executable, ok = files["sing-box.exe"]
		binaryName = "sing-box.exe"
	}
	if !ok {
		return fmt.Errorf("sing-box archive does not contain sing-box binary")
	}
	binaryPath := filepath.Join(outDir, binaryName)
	if err := writeFileIfChanged(binaryPath, executable, 0o755); err != nil {
		return fmt.Errorf("write bundled sing-box: %w", err)
	}
	if goos == "windows" {
		for _, sidecar := range []string{"wintun.dll", "libcronet.dll"} {
			if payload, ok := files[sidecar]; ok {
				if err := writeFileIfChanged(
					filepath.Join(outDir, sidecar),
					payload,
					0o644,
				); err != nil {
					return fmt.Errorf("write bundled %s: %w", sidecar, err)
				}
			}
		}
	}
	license := bundledLicenseText()
	if upstream, ok := files["license"]; ok && len(upstream) > 0 {
		license = string(upstream) + "\n\n---\n\n" + license
	}
	if err := writeFileIfChanged(
		filepath.Join(outDir, "LICENSE.sing-box.txt"),
		[]byte(license),
		0o644,
	); err != nil {
		return fmt.Errorf("write sing-box license notice: %w", err)
	}
	return nil
}

func writeFileIfChanged(path string, content []byte, mode os.FileMode) error {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, content) {
		return nil
	}
	return os.WriteFile(path, content, mode)
}

func bundledLicenseText() string {
	return `sing-box
=========

This directory includes an unmodified binary build of sing-box
(` + Version + `) from:

  ` + ProjectURL + `/releases/tag/` + Version + `

Upstream source corresponding to this binary:

  ` + ProjectURL + `/tree/` + Version + `

License: GNU General Public License v3.0 (GPL-3.0)
Full license text: https://www.gnu.org/licenses/gpl-3.0.txt

KubeLoop runs sing-box as a separate process and does not combine it into a
single binary with KubeLoop itself. See THIRD_PARTY_NOTICES.md in the KubeLoop
repository for additional distribution notes.

Windows packages may also include sidecar DLLs (for example libcronet.dll or
wintun.dll) from the same upstream release archive.
`
}
