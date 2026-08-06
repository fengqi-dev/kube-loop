//go:build ignore

// Command stage-package-assets downloads the pinned sing-box release and stages
// installer sidecars under build/bin after Wails has compiled the app.
//
// Must run as a postBuildHook: wails -clean deletes build/bin during compile,
// which would wipe anything staged by preBuildHooks.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

func main() {
	target := runtime.GOOS + "/" + runtime.GOARCH
	if len(os.Args) > 1 {
		target = os.Args[1]
	}
	goos, goarch, ok := strings.Cut(target, "/")
	if !ok || goos == "" || goarch == "" {
		fatalf("invalid target platform %q", target)
	}

	root, err := findRepositoryRoot()
	if err != nil {
		fatalf("%v", err)
	}

	binDir := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}

	fmt.Printf("==> Fetching sing-box %s for %s/%s\n", singboxdist.Version, goos, goarch)
	if err := singboxdist.BundleRelease(goos, goarch, binDir); err != nil {
		fatalf("bundle sing-box: %v", err)
	}
	fmt.Printf("==> Staged sing-box into %s\n", binDir)

	if goos == "windows" {
		if err := stageWindowsResources(root); err != nil {
			fatalf("stage windows resources: %v", err)
		}
	}
}

func stageWindowsResources(root string) error {
	embeddedDir := filepath.Join(root, "build", "embedded")
	resourcesDir := filepath.Join(root, "build", "bin", "resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return fmt.Errorf("create package resources directory: %w", err)
	}
	for _, name := range []string{
		"kubeloop-helper.exe",
		"kubeloop-helper-install.exe",
		"kubeloop-helper-uninstall.exe",
	} {
		src := filepath.Join(embeddedDir, name)
		dst := filepath.Join(resourcesDir, name)
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
		fmt.Printf("==> Staged %s for packaging\n", name)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

func findRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
