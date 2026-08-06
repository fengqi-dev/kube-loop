//go:build ignore

// Ensures sing-box is available at build/bin/sing-box for e2e helper install.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	dest := filepath.Join(root, "build", "bin", "sing-box")
	if info, err := os.Stat(dest); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
		fmt.Println(dest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	path, err := (&singboxdist.Installer{}).Ensure(ctx)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fatal(err)
	}
	if err := copyFile(path, dest); err != nil {
		fatal(err)
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		fatal(err)
	}
	fmt.Println(dest)
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
