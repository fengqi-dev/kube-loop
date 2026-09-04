package install

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
)

func TestCanReuseInstalledHelper(t *testing.T) {
	healthy := helper.Status{
		Running:   true,
		CoreReady: true,
		Version:   "dev",
		Protocol:  helperrpc.Version,
	}
	tests := []struct {
		name               string
		status             helper.Status
		enforceBinaryMatch bool
		needsBinaryUpdate  bool
		want               bool
	}{
		{
			name:              "development automatic startup permits binary drift",
			status:            healthy,
			needsBinaryUpdate: true,
			want:              true,
		},
		{
			name:               "explicit install rejects binary drift",
			status:             healthy,
			enforceBinaryMatch: true,
			needsBinaryUpdate:  true,
		},
		{
			name:               "strict install reuses matching binary",
			status:             healthy,
			enforceBinaryMatch: true,
			want:               true,
		},
		{
			name: "stopped helper is not reusable",
			status: helper.Status{
				CoreReady: true,
				Version:   "dev",
				Protocol:  helperrpc.Version,
			},
		},
		{
			name: "helper without core is not reusable",
			status: helper.Status{
				Running:  true,
				Version:  "dev",
				Protocol: helperrpc.Version,
			},
		},
		{
			name: "version mismatch is not reusable",
			status: helper.Status{
				Running:   true,
				CoreReady: true,
				Version:   "old",
				Protocol:  helperrpc.Version,
			},
		},
		{
			name: "protocol mismatch is not reusable",
			status: helper.Status{
				Running:   true,
				CoreReady: true,
				Version:   "dev",
				Protocol:  helperrpc.Version + 1,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := canReuseInstalledHelper(
				test.status,
				"dev",
				helperrpc.Version,
				test.enforceBinaryMatch,
				test.needsBinaryUpdate,
			)
			if got != test.want {
				t.Fatalf("canReuseInstalledHelper() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMustMatchBundledHelper(t *testing.T) {
	tests := []struct {
		name                 string
		requireCurrentBinary bool
		developmentBuild     bool
		want                 bool
	}{
		{name: "automatic development startup permits drift", developmentBuild: true},
		{
			name:                 "explicit development install requires match",
			requireCurrentBinary: true,
			developmentBuild:     true,
			want:                 true,
		},
		{name: "automatic release startup requires match", want: true},
		{name: "explicit release install requires match", requireCurrentBinary: true, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mustMatchBundledHelper(test.requireCurrentBinary, test.developmentBuild)
			if got != test.want {
				t.Fatalf("mustMatchBundledHelper() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMaterializeBundledHelper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	SetBundledBinary([]byte("first helper"))
	t.Cleanup(func() { SetBundledFile(helperBinaryName(helperServiceName), nil) })

	path, ok, err := materializeBundledHelper()
	if err != nil {
		t.Fatalf("materialize bundled helper: %v", err)
	}
	if !ok {
		t.Fatal("materialize bundled helper reported no embedded binary")
	}

	name := "kubeloop-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	wantPath := filepath.Join(
		home,
		".kubeloop-dev",
		"cache",
		"components",
		"dev",
		runtime.GOOS+"-"+runtime.GOARCH,
		name,
	)
	if path != wantPath {
		t.Fatalf("materialized path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(home, ".kubeloop-dev", "helper", "resources", name)); !os.IsNotExist(err) {
		t.Fatalf("legacy helper/resources path was created: %v", err)
	}
	assertFileContent(t, path, "first helper")

	SetBundledBinary([]byte("updated helper"))
	updatedPath, ok, err := materializeBundledHelper()
	if err != nil {
		t.Fatalf("update bundled helper: %v", err)
	}
	if !ok || updatedPath != path {
		t.Fatalf("updated helper = (%q, %v), want (%q, true)", updatedPath, ok, path)
	}
	assertFileContent(t, path, "updated helper")

	SetBundledBinary([]byte("concurrent helper"))
	var wait sync.WaitGroup
	errCh := make(chan error, 16)
	for range cap(errCh) {
		wait.Go(func() {
			concurrentPath, concurrentOK, concurrentErr := materializeBundledHelper()
			if concurrentErr != nil {
				errCh <- concurrentErr
				return
			}
			if !concurrentOK || concurrentPath != path {
				errCh <- fmt.Errorf("materialized helper = (%q, %v), want (%q, true)", concurrentPath, concurrentOK, path)
			}
		})
	}
	wait.Wait()
	close(errCh)
	for concurrentErr := range errCh {
		t.Error(concurrentErr)
	}
	assertFileContent(t, path, "concurrent helper")

	if err := os.WriteFile(path, []byte("replaced after materialization"), 0o700); err != nil {
		t.Fatalf("replace materialized helper: %v", err)
	}
	gotSHA256, err := bundledHelperSHA256(path)
	if err != nil {
		t.Fatalf("hash bundled helper: %v", err)
	}
	wantSHA256 := fmt.Sprintf("%x", sha256.Sum256([]byte("concurrent helper")))
	if gotSHA256 != wantSHA256 {
		t.Fatalf("bundled helper SHA-256 = %q, want %q", gotSHA256, wantSHA256)
	}
	if _, _, err := materializeBundledHelper(); err != nil {
		t.Fatalf("restore bundled helper: %v", err)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat materialized helper: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("materialized helper mode = %o, want 700", got)
		}
	}
}

func TestBundledToolCandidatesExcludeInstalledHelperOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows packages intentionally install the helper inside application resources")
	}

	name := helperBinaryName(helperServiceName)
	candidates := bundledToolCandidates(
		"/Applications/KubeLoop.app/Contents/MacOS/KubeLoop",
		"/tmp/kubeloop-source",
		name,
	)
	installed := filepath.Clean(helper.BinaryInstallPath())
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Clean(absolute) == installed {
			t.Fatalf("installed helper %q must not be a bundled source candidate", installed)
		}
	}
}

func TestLocateBundledToolReplacesStaleSupervisorCacheOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows packages intentionally use on-disk application resources")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	name := helperBinaryName(supervisorServiceName)
	SetBundledFile(name, []byte("stale embedded supervisor"))
	stalePath, ok, err := materializeBundledFile(name)
	if err != nil || !ok {
		t.Fatalf("materialize stale bundled supervisor: path=%q ok=%v err=%v", stalePath, ok, err)
	}
	SetBundledFile(name, []byte("current embedded supervisor"))
	t.Cleanup(func() { SetBundledFile(name, nil) })

	path, err := locateBundledTool(supervisorServiceName)
	if err != nil {
		t.Fatalf("locate bundled supervisor: %v", err)
	}
	want := filepath.Join(home, ".kubeloop-dev", "cache", "components", "dev", runtime.GOOS+"-"+runtime.GOARCH, name)
	if path != want {
		t.Fatalf("bundled supervisor path = %q, want materialized path %q", path, want)
	}
	assertFileContent(t, path, "current embedded supervisor")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got := string(content); got != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}

func TestWaitForHelperReadyRetriesAtInterval(t *testing.T) {
	started := time.Now()
	calls := 0
	err := waitForHelperReady(
		context.Background(),
		time.Second,
		40*time.Millisecond,
		func(context.Context) (helperrpc.Response, error) {
			calls++
			return helperrpc.Response{
				Protocol:  helperrpc.Version,
				CoreReady: calls >= 3,
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("ping calls=%d want 3", calls)
	}
	if elapsed := time.Since(started); elapsed < 70*time.Millisecond {
		t.Fatalf("helper readiness retries were not rate limited: %v", elapsed)
	}
}

func TestWaitForHelperReadyCoreNotReadyTimesOut(t *testing.T) {
	calls := 0
	err := waitForHelperReady(
		context.Background(),
		180*time.Millisecond,
		50*time.Millisecond,
		func(context.Context) (helperrpc.Response, error) {
			calls++
			return helperrpc.Response{Protocol: helperrpc.Version, CoreReady: false}, nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "bundled sing-box is not configured") {
		t.Fatalf("error=%v, want CoreReady diagnostic", err)
	}
	if calls < 3 || calls > 5 {
		t.Fatalf("ping calls=%d, want rate-limited retries", calls)
	}
}

func TestWaitForHelperReadyReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForHelperReady(
		ctx,
		time.Second,
		50*time.Millisecond,
		func(context.Context) (helperrpc.Response, error) {
			return helperrpc.Response{}, errors.New("not ready")
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
}
