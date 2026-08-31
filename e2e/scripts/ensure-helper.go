//go:build ignore

// Ensures the privileged helper is installed (may prompt for admin on macOS).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
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
	if runtime.GOOS == "darwin" {
		toolNames = append(toolNames, "kubeloop-supervisor")
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

	singBoxName := "sing-box"
	if runtime.GOOS == "windows" {
		singBoxName += ".exe"
	}
	singBox := filepath.Join(root, "build", "bin", singBoxName)
	if _, err := os.Stat(singBox); err != nil {
		command := exec.Command(
			"go", "run", "./build/singbox-patched.go",
			"-target", runtime.GOOS+"/"+runtime.GOARCH,
			"-output", singBox,
		)
		command.Dir = root
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			fatal(fmt.Errorf("build patched sing-box: %w", err))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := helperinstall.EnsureCurrentInstall(ctx); err != nil {
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
