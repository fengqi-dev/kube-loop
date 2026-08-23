package distribution

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInstallerEnsureUsesOverride(t *testing.T) {
	override := writeTestExecutable(t, "override-sing-box", []byte("override"))
	t.Setenv("KUBELOOP_SINGBOX_PATH", override)

	path, err := (&Installer{DisableDownload: true}).Ensure(t.Context())
	if err != nil || path != override {
		t.Fatalf("Ensure() path = %q, error = %v", path, err)
	}
}

func TestInstallerEnsureUsesBundledBinary(t *testing.T) {
	t.Parallel()

	bundled := writeTestExecutable(t, "bundled-sing-box", []byte("bundled"))
	path, err := (&Installer{
		BundledPath: bundled, DisableOverride: true, DisableDownload: true,
	}).Ensure(t.Context())
	if err != nil || path != bundled {
		t.Fatalf("Ensure() path = %q, error = %v", path, err)
	}
}

func TestInstallerEnsureReportsMissingBundledBinary(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing-sing-box")
	_, err := (&Installer{
		BundledPath: missing, DisableOverride: true, DisableDownload: true,
	}).Ensure(t.Context())
	if err == nil || !strings.Contains(err.Error(), missing) ||
		!strings.Contains(err.Error(), "reinstall the KubeLoop package") {
		t.Fatalf("Ensure() error = %v", err)
	}
}

func TestInstallerDownloadsVerifiedRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		goos      string
		assetName string
		files     map[string][]byte
		binary    string
	}{
		{
			name: "tar gzip", goos: "linux", assetName: "sing-box-test.tar.gz",
			files:  map[string][]byte{"release/sing-box": []byte("linux-binary")},
			binary: singBoxBinary,
		},
		{
			name: "windows zip", goos: windowsGOOS, assetName: "sing-box-test.zip",
			files: map[string][]byte{
				"release/sing-box.exe":  []byte("windows-binary"),
				"release/wintun.dll":    []byte("wintun"),
				"release/libcronet.dll": []byte("cronet"),
			},
			binary: singBoxBinaryWin,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			archive := releaseArchive(t, test.assetName, test.files)
			asset := releaseAsset{Name: test.assetName, SHA256: contentSHA256(archive)}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Header.Get("User-Agent") != "kubeloop/0.1" {
					t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
				}
				_, _ = writer.Write(archive)
			}))
			defer server.Close()

			baseDir := t.TempDir()
			installer := &Installer{
				HTTPClient: server.Client(), BaseDir: baseDir, GOOS: test.goos, GOARCH: "amd64",
				Asset: &asset, DownloadURL: func(releaseAsset) string { return server.URL },
				DisableOverride: true,
			}
			path, err := installer.Ensure(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(path) != "sing-box-"+Version+map[bool]string{true: ".exe"}[test.goos == windowsGOOS] {
				t.Fatalf("installed path = %q", path)
			}
			installed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(installed, test.files["release/"+test.binary]) {
				t.Fatalf("installed binary = %q", installed)
			}
			if test.goos == windowsGOOS {
				for _, sidecar := range []string{wintunDLL, cronetDLL} {
					content, readErr := os.ReadFile(filepath.Join(filepath.Dir(path), sidecar))
					if readErr != nil || !bytes.Equal(content, test.files["release/"+sidecar]) {
						t.Fatalf("sidecar %s = %q, error = %v", sidecar, content, readErr)
					}
				}
			}
			cached, err := installer.Ensure(t.Context())
			if err != nil || cached != path || requests.Load() != 1 {
				t.Fatalf("cached Ensure() path = %q, requests = %d, error = %v", cached, requests.Load(), err)
			}
		})
	}
}

func TestInstallerRejectsInvalidDownload(t *testing.T) {
	t.Parallel()

	archive := releaseArchive(
		t,
		"sing-box-test.tar.gz",
		map[string][]byte{"release/sing-box": []byte("binary")},
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(archive)
	}))
	defer server.Close()
	asset := releaseAsset{Name: "sing-box-test.tar.gz", SHA256: strings.Repeat("0", 64)}
	baseDir := t.TempDir()
	installer := &Installer{
		HTTPClient: server.Client(), BaseDir: baseDir, GOOS: "linux", GOARCH: "amd64",
		Asset: &asset, DownloadURL: func(releaseAsset) string { return server.URL }, DisableOverride: true,
	}
	if _, err := installer.Ensure(t.Context()); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("Ensure() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "cores", "sing-box-"+Version)); !os.IsNotExist(err) {
		t.Fatalf("unverified binary was installed: %v", err)
	}
}

func TestInstallerRejectsUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	_, err := (&Installer{
		BaseDir: t.TempDir(), GOOS: "plan9", GOARCH: "mips", DisableOverride: true,
	}).downloadToCores(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not available for plan9/mips") {
		t.Fatalf("downloadToCores() error = %v", err)
	}
}

func TestWriteExecutableReplacesDestination(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "sing-box")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeExecutable(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "new" {
		t.Fatalf("installed content = %q, error = %v", content, err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".sing-box-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, error = %v", matches, err)
	}
}

func TestInstallerPlatformOverride(t *testing.T) {
	t.Parallel()

	goos, goarch := (&Installer{GOOS: "linux", GOARCH: "arm64"}).platform()
	if goos != "linux" || goarch != "arm64" {
		t.Fatalf("platform = %s/%s", goos, goarch)
	}
}

func TestValidateBinaryRejectsDirectory(t *testing.T) {
	t.Parallel()

	if _, err := validateBinary(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("validateBinary() error = %v", err)
	}
}

func TestVerifySHA256(t *testing.T) {
	t.Parallel()

	content := []byte("verified")
	if err := verifySHA256(content, contentSHA256(content)); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256(content, strings.Repeat("0", 64)); err == nil {
		t.Fatal("verifySHA256 accepted a mismatched digest")
	}
}

func TestExtractReleaseFilesRejectsUnsupportedArchive(t *testing.T) {
	t.Parallel()

	if _, err := extractReleaseFiles("sing-box.bin", []byte("binary")); err == nil ||
		!strings.Contains(err.Error(), "unsupported sing-box archive") {
		t.Fatalf("extractReleaseFiles() error = %v", err)
	}
}

func writeTestExecutable(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseArchive(t *testing.T, name string, files map[string][]byte) []byte {
	t.Helper()
	if strings.HasSuffix(name, ".zip") {
		return zipArchive(t, files)
	}
	return tarGzipArchive(t, files)
}

func zipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func tarGzipArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func contentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
