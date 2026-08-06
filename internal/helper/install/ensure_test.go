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
)

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
	wantPath := filepath.Join(home, ".kubeloop", "helper", "resources", name)
	if path != wantPath {
		t.Fatalf("materialized path = %q, want %q", path, wantPath)
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
	errors := make(chan error, 16)
	for i := 0; i < cap(errors); i++ {
		wait.Go(func() {
			concurrentPath, concurrentOK, concurrentErr := materializeBundledHelper()
			if concurrentErr != nil {
				errors <- concurrentErr
				return
			}
			if !concurrentOK || concurrentPath != path {
				errors <- fmt.Errorf("materialized helper = (%q, %v), want (%q, true)", concurrentPath, concurrentOK, path)
			}
		})
	}
	wait.Wait()
	close(errors)
	for concurrentErr := range errors {
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

func TestLocateBundledToolPrefersEmbeddedHelperOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows packages intentionally use on-disk application resources")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	name := helperBinaryName(helperServiceName)
	SetBundledFile(name, []byte("current embedded helper"))
	t.Cleanup(func() { SetBundledFile(name, nil) })

	path, err := locateBundledTool(helperServiceName)
	if err != nil {
		t.Fatalf("locate bundled helper: %v", err)
	}
	want := filepath.Join(home, ".kubeloop", "helper", "resources", name)
	if path != want {
		t.Fatalf("bundled helper path = %q, want materialized path %q", path, want)
	}
	assertFileContent(t, path, "current embedded helper")
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
		func(context.Context) (helper.Response, error) {
			calls++
			return helper.Response{
				Protocol:  helper.ProtocolVersion,
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
		func(context.Context) (helper.Response, error) {
			calls++
			return helper.Response{Protocol: helper.ProtocolVersion, CoreReady: false}, nil
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
		func(context.Context) (helper.Response, error) {
			return helper.Response{}, errors.New("not ready")
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context.Canceled", err)
	}
}
