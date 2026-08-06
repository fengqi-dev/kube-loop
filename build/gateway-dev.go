//go:build ignore

// Command gateway-dev builds and loads the content-addressed Gateway image used
// by wails dev, then records its tag for the desktop application's initial build.
package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const imageRepository = "kube-loop-gateway"

func main() {
	root, err := findRoot()
	if err != nil {
		fatalf("%v", err)
	}
	hash, err := gatewaySourceHash(root)
	if err != nil {
		fatalf("hash Gateway sources: %v", err)
	}
	image := imageRepository + ":dev-" + hash[:12]
	binary := filepath.Join(root, "build", "bin", "kube-loop-gateway")
	contextName := currentKubeContext()

	fmt.Printf("==> Building local Gateway image %s\n", image)
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		fatalf("create build directory: %v", err)
	}
	command := exec.Command(
		"go", "build", "-trimpath", "-ldflags=-s -w",
		"-o", binary, "./cmd/kubeloop-gateway",
	)
	command.Env = append(
		os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH="+runtime.GOARCH,
	)
	if err := run(root, command); err != nil {
		fatalf("build Gateway binary: %v", err)
	}
	if err := os.Chmod(binary, 0o755); err != nil {
		fatalf("make Gateway binary executable: %v", err)
	}
	if err := buildImage(root, contextName, image); err != nil {
		fatalf("build Gateway image: %v", err)
	}
	if err := loadIntoActiveLocalCluster(root, contextName, image); err != nil {
		fatalf("load Gateway image: %v", err)
	}

	metadata := filepath.Join(root, "build", "embedded", "gateway-image")
	if err := os.MkdirAll(filepath.Dir(metadata), 0o755); err != nil {
		fatalf("create embedded metadata directory: %v", err)
	}
	if err := os.WriteFile(metadata, []byte(image+"\n"), 0o644); err != nil {
		fatalf("write Gateway image metadata: %v", err)
	}
	fmt.Printf("==> Wails development Gateway ready: %s\n", image)
}

func gatewaySourceHash(root string) (string, error) {
	paths := []string{"go.mod", "go.sum", "build/gateway.e2e.Dockerfile"}
	for _, directory := range []string{
		"cmd/kubeloop-gateway",
		"internal/gateway",
		"internal/tunnel",
	} {
		err := filepath.WalkDir(
			filepath.Join(root, directory),
			func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
					relative, err := filepath.Rel(root, path)
					if err != nil {
						return err
					}
					paths = append(paths, filepath.ToSlash(relative))
				}
				return nil
			},
		)
		if err != nil {
			return "", err
		}
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00", relative)
		_, _ = hash.Write(content)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func currentKubeContext() string {
	contextOutput, err := exec.Command("kubectl", "config", "current-context").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contextOutput))
}

func buildImage(root, contextName, image string) error {
	if isMinikubeContext(contextName) {
		profile := minikubeProfile(contextName)
		fmt.Printf("==> Building image inside Minikube profile %s\n", profile)
		if err := run(root, exec.Command(
			"minikube", "-p", profile,
			"image", "build",
			"-t", image,
			"-f", "build/gateway.e2e.Dockerfile",
			".",
		)); err != nil {
			return err
		}
		output, err := exec.Command("minikube", "-p", profile, "image", "ls").Output()
		if err != nil {
			return fmt.Errorf("list Minikube images: %w", err)
		}
		if !strings.Contains(string(output), image) {
			return fmt.Errorf("Minikube image build did not create %s", image)
		}
		return nil
	}
	return run(root, exec.Command(
		"docker", "build",
		"-t", image,
		"-f", "build/gateway.e2e.Dockerfile",
		".",
	))
}

func loadIntoActiveLocalCluster(root, contextName, image string) error {
	switch {
	case contextName == "":
		fmt.Println("==> kubectl context unavailable; image remains in the Docker daemon")
		return nil
	case isMinikubeContext(contextName):
		return nil
	case strings.HasPrefix(contextName, "kind-"):
		return run(root, exec.Command(
			"kind", "load", "docker-image", image,
			"--name", strings.TrimPrefix(contextName, "kind-"),
		))
	case strings.HasPrefix(contextName, "k3d-"):
		return run(root, exec.Command(
			"k3d", "image", "import", image,
			"--cluster", strings.TrimPrefix(contextName, "k3d-"),
		))
	default:
		fmt.Printf("==> Using Docker image directly for Kubernetes context %s\n", contextName)
		return nil
	}
}

func isMinikubeContext(contextName string) bool {
	return contextName == "minikube" || strings.HasPrefix(contextName, "minikube-")
}

func minikubeProfile(contextName string) string {
	if contextName == "minikube" {
		return contextName
	}
	return strings.TrimPrefix(contextName, "minikube-")
}

func run(directory string, command *exec.Cmd) error {
	command.Dir = directory
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s: %w", command.String(), err)
	}
	return nil
}

func findRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		directory = parent
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
