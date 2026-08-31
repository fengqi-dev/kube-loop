//go:build ignore

// Ensures sing-box is available at the platform-native path under build/bin
// for e2e helper install.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	name := "sing-box"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(root, "build", "bin", name)
	if info, err := os.Stat(dest); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
		fmt.Println(dest)
		return
	}

	command := exec.Command(
		"go", "run", "./build/singbox-patched.go",
		"-target", runtime.GOOS+"/"+runtime.GOARCH,
		"-output", dest,
	)
	command.Dir = root
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		fatal(fmt.Errorf("build patched sing-box: %w", err))
	}
	if info, err := os.Stat(dest); err != nil || !info.Mode().IsRegular() ||
		(runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		fatal(fmt.Errorf("bundled sing-box is unavailable at %s", dest))
	}
	if err := os.Chmod(dest, 0o755); err != nil && runtime.GOOS != "windows" {
		fatal(err)
	}
	fmt.Println(dest)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
