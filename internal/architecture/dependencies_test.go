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

func TestOperatorReconcilerUsesDistinctPackageName(t *testing.T) {
	root := repositoryRoot(t)
	operatorRoot := filepath.Join(root, "internal", "operator")
	topLevelOperator := filepath.Join(root, "operator")
	if _, err := os.Stat(topLevelOperator); err == nil {
		t.Fatalf("Operator component must live below internal: %s", topLevelOperator)
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect top-level Operator directory: %v", err)
	}

	legacy := filepath.Join(root, "internal", "operator", "controller")
	if _, err := os.Stat(legacy); err == nil {
		t.Fatalf("Operator reconciler must not reuse the application controller package name: %s", legacy)
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect legacy Operator controller package: %v", err)
	}

	directory := filepath.Join(root, "internal", "operator", "trafficbinding")
	packages, err := parser.ParseDir(token.NewFileSet(), directory, nil, parser.PackageClauseOnly)
	if err != nil {
		t.Fatalf("parse Operator reconciler package: %v", err)
	}
	if len(packages) != 1 || packages["trafficbinding"] == nil {
		names := make([]string, 0, len(packages))
		for name := range packages {
			names = append(names, name)
		}
		slices.Sort(names)
		t.Fatalf("Operator reconciler package must be named trafficbinding, got %v", names)
	}

	legacyCommand := filepath.Join(root, "internal", "operator", "cmd")
	if _, err := os.Stat(legacyCommand); err == nil {
		t.Fatalf("Operator command must live under the root cmd directory: %s", legacyCommand)
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect legacy Operator command: %v", err)
	}
	operatorCommand := filepath.Join(root, "cmd", "kubeloop-operator")
	commandPackages, err := parser.ParseDir(token.NewFileSet(), operatorCommand, nil, parser.PackageClauseOnly)
	if err != nil {
		t.Fatalf("parse Operator command package: %v", err)
	}
	if len(commandPackages) != 1 || commandPackages["main"] == nil {
		t.Fatalf("Operator command must be a main package")
	}

	entries, err := os.ReadDir(operatorRoot)
	if err != nil {
		t.Fatalf("read internal Operator directory: %v", err)
	}
	allowed := map[string]bool{"api": true, "trafficbinding": true}
	for _, entry := range entries {
		if !entry.IsDir() || !allowed[entry.Name()] {
			t.Errorf("internal/operator must contain only Operator Go packages; move project artifact %q to the repository root", entry.Name())
		}
	}
}

func TestWireProtocolPackagesLiveUnderProtocol(t *testing.T) {
	root := repositoryRoot(t)
	packages := []string{
		"exchangestream",
		"execstream",
		"filestream",
		"helper",
		"mirrorstream",
		"networkspec",
		"relaycontrol",
		"relayticket",
		"tunnel",
		"wssprotocol",
	}

	for _, name := range packages {
		t.Run(name, func(t *testing.T) {
			protocolDirectory := filepath.Join(root, "internal", "protocol", name)
			entries, err := os.ReadDir(protocolDirectory)
			if err != nil {
				t.Fatalf("read protocol package: %v", err)
			}
			hasSource := false
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") &&
					!strings.HasSuffix(entry.Name(), "_test.go") {
					hasSource = true
					break
				}
			}
			if !hasSource {
				t.Fatalf("%s has no non-test Go source", protocolDirectory)
			}

			if name == "helper" {
				legacyProtocol := filepath.Join(root, "internal", "helper", "protocol.go")
				if _, err := os.Stat(legacyProtocol); err == nil {
					t.Errorf("helper wire contract must live under internal/protocol: %s", legacyProtocol)
				} else if !os.IsNotExist(err) {
					t.Fatalf("inspect legacy helper protocol: %v", err)
				}
				return
			}

			legacyDirectory := filepath.Join(root, "internal", name)
			legacyEntries, err := os.ReadDir(legacyDirectory)
			if os.IsNotExist(err) {
				return
			}
			if err != nil {
				t.Fatalf("read legacy protocol package: %v", err)
			}
			for _, entry := range legacyEntries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
					t.Errorf("protocol source must live under internal/protocol: %s", filepath.Join(legacyDirectory, entry.Name()))
				}
			}
		})
	}
}

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

func TestV2ControllerDoesNotImportV1DesktopOrDataPlaneRuntime(t *testing.T) {
	root := repositoryRoot(t)
	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/cluster",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		modulePath + "/internal/session",
		modulePath + "/internal/store",
		"github.com/wailsapp/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "controller"), forbidden)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "cmd", "kubeloop-controller"), forbidden)
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

func TestDataPlaneDoesNotImportControlPlaneOrKubernetesRuntime(t *testing.T) {
	root := repositoryRoot(t)
	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/cluster",
		modulePath + "/internal/controller",
		modulePath + "/internal/helper",
		modulePath + "/internal/store",
		"github.com/wailsapp/",
		"github.com/jackc/pgx/",
		"golang.org/x/oauth2",
		"k8s.io/",
		"modernc.org/sqlite",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "gateway"), forbidden)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "cmd", "kubeloop-gateway"), forbidden)
}

func TestV2ClientSDKDoesNotImportLocalKubernetesOrServerRuntime(t *testing.T) {
	root := repositoryRoot(t)
	forbidden := []string{
		modulePath + "/internal/cluster",
		modulePath + "/internal/controller",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		modulePath + "/internal/session",
		modulePath + "/internal/store",
		"github.com/wailsapp/",
		"k8s.io/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "clientv2"), forbidden)
}

func TestDesktopCompositionRootDoesNotImportV1KubernetesRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/cluster",
		modulePath + "/internal/filemanager",
		modulePath + "/internal/gateway",
		modulePath + "/internal/session",
		modulePath + "/internal/store",
		"k8s.io/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(repositoryRoot(t), "internal", "app"), forbidden)
}

func TestV2UIAndWailsBindingsDoNotImplementTransportProtocol(t *testing.T) {
	root := repositoryRoot(t)
	assertTreeContentsDoNotContain(t, filepath.Join(root, "frontend", "src", "components", "server"), []string{
		"fetch(", "new WebSocket(", "JSON.parse(", "/api/v2", "/auth/",
	})
	entries, err := os.ReadDir(filepath.Join(root, "internal", "app"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "bindings_v2") ||
			!strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		assertFileContentsDoNotContain(t, filepath.Join(root, "internal", "app", entry.Name()), []string{
			"net/http", "coder/websocket", "/api/v2", "/auth/",
		})
	}
}

func TestV2MCPDoesNotImportLocalKubernetesOrV1DesktopRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/cluster",
		modulePath + "/internal/filemanager",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		modulePath + "/internal/intercept",
		modulePath + "/internal/session",
		modulePath + "/internal/store",
		"k8s.io/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(repositoryRoot(t), "internal", "mcp"), forbidden)
}

func TestDesktopDependencyGraphDoesNotContainKubernetes(t *testing.T) {
	root := repositoryRoot(t)
	command := exec.Command("go", "list", "-deps", ".")
	command.Dir = root
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("list desktop dependencies: %v", err)
	}
	for dependency := range strings.FieldsSeq(string(raw)) {
		if strings.HasPrefix(dependency, "k8s.io/") ||
			dependency == modulePath+"/internal/cluster" ||
			dependency == modulePath+"/internal/session" {
			t.Fatalf("desktop dependency graph contains forbidden package %q", dependency)
		}
	}
}

func TestKubernetesDirectImportInventoryIsExhaustive(t *testing.T) {
	root := repositoryRoot(t)
	want := []string{
		"cmd/kubeloop-controller",
		"cmd/kubeloop-operator",
		"internal/cluster",
		"internal/cluster/discovery",
		"internal/cluster/gatewayruntime",
		"internal/cluster/inventory",
		"internal/cluster/kubeportforward",
		"internal/cluster/podexec",
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
		"internal/intercept/clusteradapter",
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
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
				strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			imports, err := fileImports(path)
			if err != nil {
				return err
			}
			for _, importPath := range imports {
				if !strings.HasPrefix(importPath, "k8s.io/") {
					continue
				}
				directory, err := filepath.Rel(root, filepath.Dir(path))
				if err != nil {
					return err
				}
				seen[filepath.ToSlash(directory)] = struct{}{}
				break
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s Kubernetes imports: %v", tree, err)
		}
	}

	got := make([]string, 0, len(seen))
	for directory := range seen {
		got = append(got, directory)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("direct Kubernetes import inventory changed\n got: %q\nwant: %q\nupdate docs/v2-kubernetes-call-sites.zh-CN.md and this reviewed inventory together", got, want)
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
	imports, err := fileImports(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, importPath := range imports {
		for _, prefix := range forbidden {
			if strings.HasPrefix(importPath, prefix) {
				t.Errorf("%s imports forbidden infrastructure package %q", path, importPath)
			}
		}
	}
}

func assertTreeContentsDoNotContain(t *testing.T, directory string, forbidden []string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".ts") && !strings.HasSuffix(entry.Name(), ".tsx")) {
			return nil
		}
		assertFileContentsDoNotContain(t, path, forbidden)
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
	contents := string(raw)
	for _, value := range forbidden {
		if strings.Contains(contents, value) {
			t.Errorf("%s contains forbidden transport implementation %q", path, value)
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
