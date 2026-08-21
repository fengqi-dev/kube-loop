//go:build ignore

// Command helper-prebuild builds the platform helper before Wails compiles the
// desktop application so it can be embedded from build/embedded.
//
// Wails runs build hooks from build/bin. Package assets that must live next to
// the final binary (sing-box, Windows resources/) are staged in
// stage-package-assets.go via postBuildHooks — wails -clean wipes build/bin
// during CompileProject after preBuildHooks.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
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
		}{pkg: "./cmd/kubeloop-supervisor", name: "kubeloop-supervisor", version: supervisorprotocol.BinaryVersion})
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
			"-trimpath",
			"-ldflags", "-s -w -X main.version=" + target.version,
			"-o", output,
			target.pkg,
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Env = setEnvironment(os.Environ(), map[string]string{
			"CGO_ENABLED": "0",
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
