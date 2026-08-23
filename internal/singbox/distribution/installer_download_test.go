package distribution

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

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
			expectedName := "sing-box-" + Version
			if test.goos == windowsGOOS {
				expectedName += ".exe"
			}
			if filepath.Base(path) != expectedName {
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
	}).downloadToCores(t.Context())
	if err == nil || !strings.Contains(err.Error(), "not available for plan9/mips") {
		t.Fatalf("downloadToCores() error = %v", err)
	}
}
