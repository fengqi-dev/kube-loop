package architecture

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSupervisorBinaryDoesNotFollowApplicationVersion(t *testing.T) {
	root := repositoryRoot(t)
	first := buildDarwinRuntimeComponents(t, root, "v9.1.0")
	second := buildDarwinRuntimeComponents(t, root, "v9.2.0")

	firstHelper := fileDigest(t, filepath.Join(first, "kubeloop-helper"))
	secondHelper := fileDigest(t, filepath.Join(second, "kubeloop-helper"))
	if firstHelper == secondHelper {
		t.Fatal("helper binary did not follow the application version")
	}
	firstSupervisor := fileDigest(t, filepath.Join(first, "kubeloop-supervisor"))
	secondSupervisor := fileDigest(t, filepath.Join(second, "kubeloop-supervisor"))
	if firstSupervisor != secondSupervisor {
		t.Fatal("supervisor binary changed with the application version")
	}
}

func buildDarwinRuntimeComponents(t *testing.T, root, version string) string {
	t.Helper()
	output := t.TempDir()
	command := exec.Command(
		"go", "run", "./build/helper-prebuild.go",
		"darwin/"+runtime.GOARCH, output,
	)
	command.Dir = root
	command.Env = append(os.Environ(), "VITE_APP_VERSION="+version)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build runtime components for %s: %v\n%s", version, err, raw)
	}
	return output
}

func fileDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(fmt.Errorf("read %s: %w", path, err))
	}
	return sha256.Sum256(content)
}
