package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/fengqi-dev/kube-loop"

func TestCoreFeaturePackagesDoNotImportInfrastructure(t *testing.T) {
	root := repositoryRoot(t)
	rules := []struct {
		path      string
		forbidden []string
	}{
		{
			path: "internal/intercept",
			forbidden: []string{
				modulePath + "/internal/cluster",
				"k8s.io/",
			},
		},
		{
			path: "internal/portfwd",
			forbidden: []string{
				modulePath + "/internal/cluster",
				"k8s.io/",
			},
		},
		{
			path:      "internal/session",
			forbidden: []string{"k8s.io/"},
		},
		{
			path:      "internal/traffic",
			forbidden: []string{modulePath + "/internal/"},
		},
	}

	for _, rule := range rules {
		t.Run(rule.path, func(t *testing.T) {
			assertImportsDoNotMatch(t, filepath.Join(root, rule.path), rule.forbidden)
		})
	}
}

func TestSessionLayerDoesNotImportKubernetes(t *testing.T) {
	assertTreeImportsDoNotMatch(
		t,
		filepath.Join(repositoryRoot(t), "internal", "session"),
		[]string{"k8s.io/"},
	)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func assertImportsDoNotMatch(t *testing.T, directory string, forbidden []string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		parsed, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", path, err)
			}
			for _, prefix := range forbidden {
				if strings.HasPrefix(importPath, prefix) {
					t.Errorf("%s imports forbidden infrastructure package %q", path, importPath)
				}
			}
		}
	}
}

func assertTreeImportsDoNotMatch(t *testing.T, directory string, forbidden []string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		assertFileImportsDoNotMatch(t, path, forbidden)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertFileImportsDoNotMatch(t *testing.T, path string, forbidden []string) {
	t.Helper()
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("decode import in %s: %v", path, err)
		}
		for _, prefix := range forbidden {
			if strings.HasPrefix(importPath, prefix) {
				t.Errorf("%s imports forbidden infrastructure package %q", path, importPath)
			}
		}
	}
}
