//go:build ignore

// Command singbox-patched builds the pinned sing-box source after applying the
// repository-owned patch set to an isolated temporary source tree.
package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	singboxdist "github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
)

func main() {
	var checkOnly bool
	var target string
	var output string
	flag.BoolVar(&checkOnly, "check", false, "verify the pinned source and patch without building")
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
	if err := verifyPinnedSource(sourceDir); err != nil {
		fatalf("%v", err)
	}
	patchPaths, err := filepath.Glob(filepath.Join(root, "third_party", "patches", "sing-box", "*.patch"))
	if err != nil || len(patchPaths) == 0 {
		fatalf("find sing-box patches: %v", err)
	}
	workDir, err := os.MkdirTemp("", "kubeloop-sing-box-")
	if err != nil {
		fatalf("create temporary sing-box source tree: %v", err)
	}
	defer os.RemoveAll(workDir)
	if err := exportSource(sourceDir, workDir); err != nil {
		fatalf("export sing-box source: %v", err)
	}
	for _, patchPath := range patchPaths {
		if err := applyPatch(workDir, patchPath); err != nil {
			fatalf("apply sing-box patch %s: %v", filepath.Base(patchPath), err)
		}
	}
	if checkOnly {
		fmt.Printf(
			"==> %d sing-box patches apply cleanly to %s (%s)\n",
			len(patchPaths),
			singboxdist.Version,
			singboxdist.SourceRevision,
		)
		return
	}

	name := "sing-box"
	buildTagsFile := "DEFAULT_BUILD_TAGS_OTHERS"
	if goos == "windows" {
		name += ".exe"
		buildTagsFile = "DEFAULT_BUILD_TAGS_WINDOWS"
	}
	if output == "" {
		output = filepath.Join(root, "build", "bin", name)
	} else if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	tags, err := readBuildSetting(filepath.Join(workDir, "release", buildTagsFile))
	if err != nil {
		fatalf("read sing-box build tags: %v", err)
	}
	sharedLDFlags, err := readBuildSetting(filepath.Join(workDir, "release", "LDFLAGS"))
	if err != nil {
		fatalf("read sing-box linker flags: %v", err)
	}
	version := strings.TrimPrefix(singboxdist.Version, "v")
	ldflags := strings.TrimSpace(
		"-s -w -X github.com/sagernet/sing-box/constant.Version=" + version + " " + sharedLDFlags,
	)
	args := []string{
		"build", "-buildvcs=false", "-trimpath", "-tags", tags, "-ldflags", ldflags,
		"-o", output, "./cmd/sing-box",
	}
	command := exec.Command("go", args...)
	command.Dir = workDir
	command.Env = setEnvironment(os.Environ(), map[string]string{
		"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch, "GOTOOLCHAIN": "local",
	})
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	fmt.Printf("==> Building patched sing-box %s for %s/%s (tags=%s)\n", singboxdist.Version, goos, goarch, tags)
	if err := command.Run(); err != nil {
		fatalf("build patched sing-box: %v", err)
	}
	if goos != "windows" {
		if err := os.Chmod(output, 0o755); err != nil {
			fatalf("mark patched sing-box executable: %v", err)
		}
	}
	license, err := os.ReadFile(filepath.Join(workDir, "LICENSE"))
	if err != nil {
		fatalf("read sing-box license: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(output), "LICENSE.sing-box.txt"), license, 0o644); err != nil {
		fatalf("write sing-box license: %v", err)
	}
	if goos == "windows" {
		if err := stageWindowsSidecars(workDir, goarch, filepath.Dir(output)); err != nil {
			fatalf("stage Windows sing-box sidecars: %v", err)
		}
	}
	fmt.Printf("==> Patched sing-box staged at %s\n", output)
}

func stageWindowsSidecars(sourceDir, goarch, outputDir string) error {
	if goarch != "amd64" && goarch != "arm64" {
		return fmt.Errorf("unsupported Windows architecture %q", goarch)
	}
	dependencies := []struct {
		module string
		path   string
		name   string
	}{
		{
			module: "github.com/sagernet/sing-tun",
			path:   filepath.Join("internal", "wintun", goarch, "wintun.dll"),
			name:   "wintun.dll",
		},
		{
			module: "github.com/sagernet/cronet-go/lib/windows_" + goarch,
			path:   "libcronet.dll",
			name:   "libcronet.dll",
		},
	}
	for _, dependency := range dependencies {
		moduleDir, err := goModuleDir(sourceDir, dependency.module)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(filepath.Join(moduleDir, dependency.path))
		if err != nil {
			return fmt.Errorf("read %s from %s: %w", dependency.name, dependency.module, err)
		}
		if err := os.WriteFile(filepath.Join(outputDir, dependency.name), content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dependency.name, err)
		}
		fmt.Printf("==> Staged %s from %s\n", dependency.name, dependency.module)
	}
	return nil
}

func goModuleDir(sourceDir, module string) (string, error) {
	command := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module)
	command.Dir = sourceDir
	command.Env = setEnvironment(os.Environ(), map[string]string{
		"GOTOOLCHAIN": "local",
	})
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("locate Go module %s: %w", module, err)
	}
	dir := strings.TrimSpace(string(output))
	if dir == "" {
		return "", fmt.Errorf("Go module %s has no source directory", module)
	}
	return dir, nil
}

func verifyPinnedSource(sourceDir string) error {
	if _, err := os.Stat(filepath.Join(sourceDir, "go.mod")); err != nil {
		return errors.New("sing-box source is not initialized; run git submodule update --init --recursive")
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = sourceDir
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != singboxdist.SourceRevision {
		return fmt.Errorf("sing-box source must be pinned at revision %s", singboxdist.SourceRevision)
	}

	// GitHub Actions checks submodules out at the recorded commit without
	// fetching tag objects. The immutable revision is therefore authoritative.
	// When the expected tag is available locally, also verify that it resolves
	// to the same commit so a moved or incorrect tag cannot pass unnoticed.
	tagRef := "refs/tags/" + singboxdist.Version
	command = exec.Command("git", "show-ref", "--verify", "--quiet", tagRef)
	command.Dir = sourceDir
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			return fmt.Errorf("inspect sing-box source tag %s: %w", singboxdist.Version, err)
		}
		return nil
	}
	command = exec.Command("git", "rev-parse", tagRef+"^{commit}")
	command.Dir = sourceDir
	output, err = command.Output()
	if err != nil || strings.TrimSpace(string(output)) != singboxdist.SourceRevision {
		return fmt.Errorf(
			"sing-box tag %s must resolve to revision %s",
			singboxdist.Version,
			singboxdist.SourceRevision,
		)
	}
	return nil
}

func exportSource(sourceDir, destination string) error {
	command := exec.Command("git", "archive", "--format=tar", "HEAD")
	command.Dir = sourceDir
	contents, err := command.Output()
	if err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(contents))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("sing-box archive contains an unsafe path")
		}
		path := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// Metadata-only PAX records do not materialize a filesystem entry.
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("sing-box archive contains unsupported entry %q", header.Name)
		}
	}
}

func applyPatch(dir, path string) error {
	patch, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Git may check text files out with CRLF on Windows, while git archive
	// preserves the upstream blobs' LF line endings. Feed git apply a canonical
	// patch stream so the same repository revision builds on every runner.
	patch = bytes.ReplaceAll(patch, []byte("\r\n"), []byte("\n"))
	command := exec.Command("git", "apply", "--whitespace=nowarn", "--unidiff-zero", "-")
	command.Dir = dir
	command.Stdin = bytes.NewReader(patch)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
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
			return "", errors.New("go.mod not found above working directory")
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
