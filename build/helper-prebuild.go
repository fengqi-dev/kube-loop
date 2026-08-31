//go:build ignore

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

func main() {
	if len(os.Args) > 3 {
		fatalf("usage: helper-prebuild [goos/goarch] [output-directory]")
	}
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

	version := os.Getenv("VITE_APP_VERSION")
	if version == "" {
		version = "dev"
	}

	targets := []struct {
		pkg     string
		name    string
		version string
	}{
		{pkg: "./cmd/kubeloop-helper", name: "kubeloop-helper", version: version},
	}
	if goos == "darwin" {
		targets = append(targets, struct {
			pkg     string
			name    string
			version string
		}{pkg: "./cmd/kubeloop-supervisor", name: "kubeloop-supervisor", version: supervisor.BinaryVersion})
	}

	embeddedDir := filepath.Join(root, "build", "embedded")
	if len(os.Args) > 2 {
		embeddedDir = os.Args[2]
	}
	if err := os.MkdirAll(embeddedDir, 0o755); err != nil {
		fatalf("create embedded helper directory: %v", err)
	}
	for _, target := range targets {
		name := target.name
		if goos == "windows" {
			name += ".exe"
		}
		output := filepath.Join(embeddedDir, name)
		args := []string{
			"build",
			"-buildvcs=false",
			"-trimpath",
			"-ldflags", "-s -w -X github.com/fengqi-dev/kube-loop/internal/buildinfo.version=" + target.version,
			"-o", output,
			target.pkg,
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cgoEnabled := "0"
		if goos == "darwin" && target.name == "kubeloop-helper" {
			// The macOS helper performs system trust changes from the same root
			// process that installs the service. Security.framework requires cgo.
			cgoEnabled = "1"
		}
		cmd.Env = setEnvironment(os.Environ(), map[string]string{
			"CGO_ENABLED": cgoEnabled,
			"GOOS":        goos,
			"GOARCH":      goarch,
		})
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		fmt.Printf("==> Building %s for %s/%s (version=%s)\n", name, goos, goarch, target.version)
		if err := cmd.Run(); err != nil {
			fatalf("build %s: %v", name, err)
		}
	}
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

func setEnvironment(current []string, values map[string]string) []string {
	result := make([]string, 0, len(current)+len(values))
	for _, entry := range current {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[strings.ToUpper(key)]; !replaced {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
