package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/fengqi-dev/kube-loop"

func TestControllerDoesNotImportDesktopOrDataPlaneRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/client",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		"github.com/wailsapp/",
	}
	root := repositoryRoot(t)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "controller"), forbidden)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "cmd", "kubeloop-controller"), forbidden)
}

func TestGatewayDoesNotImportControlPlaneOrDesktopRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/client",
		modulePath + "/internal/controller",
		modulePath + "/internal/helper",
		"github.com/wailsapp/",
		"github.com/jackc/pgx/",
		"golang.org/x/oauth2",
		"k8s.io/",
		"modernc.org/sqlite",
	}
	root := repositoryRoot(t)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "gateway"), forbidden)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "cmd", "kubeloop-gateway"), forbidden)
}

func TestClientDoesNotImportKubernetesOrServerRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/controller",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		"github.com/wailsapp/",
		"k8s.io/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(repositoryRoot(t), "internal", "client"), forbidden)
}

func TestDesktopCompositionDoesNotImportKubernetes(t *testing.T) {
	root := repositoryRoot(t)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "app"), []string{"k8s.io/"})
	command := exec.Command("go", "list", "-deps", ".")
	command.Dir = root
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("list desktop dependencies: %v", err)
	}
	for dependency := range strings.FieldsSeq(string(raw)) {
		if strings.HasPrefix(dependency, "k8s.io/") {
			t.Fatalf("desktop dependency graph contains Kubernetes package %q", dependency)
		}
	}
}

func TestMCPDoesNotImportKubernetesOrServerRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/controller",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		"k8s.io/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(repositoryRoot(t), "internal", "mcp"), forbidden)
}

func TestTrafficWorkflowsDelegateKubernetesWritesToOperator(t *testing.T) {
	root := repositoryRoot(t)
	legacyWrites := []string{
		"ApplyCapturedServiceIntercept",
		"RestoreServiceIntercept",
		"CreatePreviewService",
		"DeletePreviewService",
	}
	for _, path := range []string{
		"internal/controller/exchangeapi/mutator.go",
		"internal/controller/mirrorapi/mutator.go",
		"internal/controller/previewapi/resources.go",
		"internal/controller/portforwardapi/binding.go",
	} {
		assertFileContentsDoNotContain(t, filepath.Join(root, path), legacyWrites)
	}

	mainPath := filepath.Join(root, "cmd", "kubeloop-controller", "main.go")
	raw, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(raw)
	for _, constructor := range []string{
		"exchangeapi.NewTrafficBindingResourceMutator",
		"mirrorapi.NewTrafficBindingResourceMutator",
		"previewapi.NewTrafficBindingResourceManager",
		"portforwardapi.NewTrafficBindingManager",
	} {
		if !strings.Contains(contents, constructor) {
			t.Errorf("%s must compose %s", mainPath, constructor)
		}
	}
}

func TestKubernetesDirectImportInventoryIsExhaustive(t *testing.T) {
	root := repositoryRoot(t)
	want := []string{
		"cmd/kubeloop-controller",
		"cmd/kubeloop-operator",
		"internal/controller/exchangeapi",
		"internal/controller/execapi",
		"internal/controller/fileapi",
		"internal/controller/fileopsapi",
		"internal/controller/kubeapi",
		"internal/controller/kubernetes",
		"internal/controller/mirrorapi",
		"internal/controller/networkapi",
		"internal/controller/portforwardapi",
		"internal/controller/previewapi",
		"internal/controller/relayregistry",
		"internal/controller/sessionapi",
		"internal/controller/trafficbindingclient",
		"internal/operator/api/v1alpha1",
		"internal/operator/trafficbinding",
		"internal/servicebinding",
	}

	seen := make(map[string]struct{}, len(want))
	for _, tree := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			imports, err := fileImports(path)
			if err != nil {
				return err
			}
			if slices.ContainsFunc(imports, func(importPath string) bool { return strings.HasPrefix(importPath, "k8s.io/") }) {
				directory, err := filepath.Rel(root, filepath.Dir(path))
				if err != nil {
					return err
				}
				seen[filepath.ToSlash(directory)] = struct{}{}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s Kubernetes imports: %v", tree, err)
		}
	}

	got := mapsKeys(seen)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("direct Kubernetes import inventory changed\n got: %q\nwant: %q", got, want)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func assertTreeImportsDoNotMatch(t *testing.T, directory string, forbidden []string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		imports, err := fileImports(path)
		if err != nil {
			return err
		}
		for _, importPath := range imports {
			for _, prefix := range forbidden {
				if strings.HasPrefix(importPath, prefix) {
					t.Errorf("%s imports forbidden package %q", path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertFileContentsDoNotContain(t *testing.T, path string, forbidden []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if strings.Contains(string(raw), value) {
			t.Errorf("%s contains forbidden implementation %q", path, value)
		}
	}
}

func fileImports(path string) ([]string, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, importPath)
	}
	return imports, nil
}

func mapsKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
