//go:build ignore

// Ensures sing-box is available at the platform-native path under build/bin
// for e2e helper install.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
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

	if err := singboxdist.BundleRelease(runtime.GOOS, runtime.GOARCH, filepath.Dir(dest)); err != nil {
		fatal(err)
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
