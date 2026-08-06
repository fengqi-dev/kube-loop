//go:build ignore

// Ensures the privileged helper is installed (may prompt for admin on macOS).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	helperName := "kubeloop-helper"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	toolNames := []string{helperName}
	if runtime.GOOS == "windows" {
		toolNames = append(toolNames,
			"kubeloop-helper-install.exe",
			"kubeloop-helper-uninstall.exe",
		)
	}
	toolDir := filepath.Join(root, "build", "bin")
	if runtime.GOOS == "windows" {
		// Windows package resources take precedence in LocateBundled*.
		toolDir = filepath.Join(toolDir, "resources")
	}
	for _, name := range toolNames {
		src := filepath.Join(root, "build", "embedded", name)
		if _, err := os.Stat(src); err != nil {
			fatal(fmt.Errorf("helper binary missing at %s; run ./build/bundle-helper.sh", src))
		}
		dest := filepath.Join(toolDir, name)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			fatal(err)
		}
		if err := copyFile(src, dest); err != nil {
			fatal(err)
		}
		_ = os.Chmod(dest, 0o755)
	}

	singBox := filepath.Join(root, "build", "bin", "sing-box")
	if _, err := os.Stat(singBox); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		path, ensureErr := (&singboxdist.Installer{}).Ensure(ctx)
		cancel()
		if ensureErr != nil {
			fatal(ensureErr)
		}
		if err := copyFile(path, singBox); err != nil {
			fatal(err)
		}
		_ = os.Chmod(singBox, 0o755)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := helperinstall.EnsureInstall(ctx); err != nil {
		fatal(err)
	}
	fmt.Println("helper ready")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
