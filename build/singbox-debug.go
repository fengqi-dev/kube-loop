//go:build ignore

// Command singbox-debug builds the pinned sing-box submodule with compiler
// optimizations and inlining disabled so a debugger can use source breakpoints.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

func main() {
	var target string
	var output string
	flag.StringVar(&target, "target", runtime.GOOS+"/"+runtime.GOARCH, "target platform (GOOS/GOARCH)")
	flag.StringVar(&output, "output", "", "output binary path")
	flag.Parse()

	goos, goarch, ok := strings.Cut(target, "/")
	if !ok || goos == "" || goarch == "" {
		fatalf("invalid target platform %q", target)
	}

	root, err := findRepositoryRoot()
	if err != nil {
		fatalf("%v", err)
	}
	sourceDir := filepath.Join(root, "third_party", "sing-box")
	if _, err := os.Stat(filepath.Join(sourceDir, "go.mod")); err != nil {
		fatalf("sing-box source is not initialized; run git submodule update --init --recursive")
	}

	name := "sing-box"
	if goos == "windows" {
		name += ".exe"
	}
	if output == "" {
		output = filepath.Join(root, "build", "bin", name)
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}

	tags, err := readBuildSetting(filepath.Join(sourceDir, "release", "DEFAULT_BUILD_TAGS_OTHERS"))
	if err != nil {
		fatalf("read sing-box build tags: %v", err)
	}
	sharedLDFlags, err := readBuildSetting(filepath.Join(sourceDir, "release", "LDFLAGS"))
	if err != nil {
		fatalf("read sing-box linker flags: %v", err)
	}
	version := strings.TrimPrefix(singboxdist.Version, "v")
	ldflags := strings.TrimSpace(
		"-X github.com/sagernet/sing-box/constant.Version=" + version + " " + sharedLDFlags,
	)

	args := []string{
		"build",
		"-buildvcs=false",
		"-tags", tags,
		"-gcflags=all=-N -l",
		"-ldflags", ldflags,
		"-o", output,
		"./cmd/sing-box",
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = sourceDir
	cmd.Env = setEnvironment(os.Environ(), map[string]string{
		"CGO_ENABLED": "0",
		"GOOS":        goos,
		"GOARCH":      goarch,
		"GOTOOLCHAIN": "local",
	})
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf(
		"==> Building debug sing-box %s from %s for %s/%s\n",
		singboxdist.Version, sourceRevision(sourceDir), goos, goarch,
	)
	if err := cmd.Run(); err != nil {
		fatalf("build debug sing-box: %v", err)
	}
	if goos != "windows" {
		if err := os.Chmod(output, 0o755); err != nil {
			fatalf("mark debug sing-box executable: %v", err)
		}
	}

	license, err := os.ReadFile(filepath.Join(sourceDir, "LICENSE"))
	if err != nil {
		fatalf("read sing-box license: %v", err)
	}
	licensePath := filepath.Join(filepath.Dir(output), "LICENSE.sing-box.txt")
	if err := os.WriteFile(licensePath, license, 0o644); err != nil {
		fatalf("write sing-box license: %v", err)
	}
	fmt.Printf("==> Debug sing-box staged at %s\n", output)
}

func readBuildSetting(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	setting := strings.TrimSpace(string(value))
	if setting == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return setting, nil
}

func sourceRevision(sourceDir string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = sourceDir
	value, err := cmd.Output()
	if err != nil {
		return "unknown revision"
	}
	return strings.TrimSpace(string(value))
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
