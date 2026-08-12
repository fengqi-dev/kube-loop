package architecture

import (
	"errors"
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

func TestControlPlaneAPIModulesKeepTransportServiceAndModelsSeparated(t *testing.T) {
	root := repositoryRoot(t)
	transportModules := []string{
		"exchangeapi",
		"execapi",
		"fileapi",
		"fileopsapi",
		"kubeapi",
		"mirrorapi",
		"previewapi",
	}
	for _, module := range transportModules {
		directory := filepath.Join(root, "internal", "controlplane", filepath.FromSlash(module))
		for _, name := range []string{"routes.go", "service.go", "dto.go"} {
			path := filepath.Join(directory, name)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("API module %s must contain %s: %v", module, name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(directory, "handler.go")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("API module %s must not contain the legacy handler.go", module)
		}
		if _, err := os.Stat(filepath.Join(directory, "model.go")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("API module %s must not mix model.go with transport and service code", module)
		}
		imports, err := fileImports(filepath.Join(directory, "dto.go"))
		if err != nil {
			t.Errorf("read DTO imports for %s: %v", module, err)
		} else if slices.Contains(imports, "github.com/labstack/echo/v5") {
			t.Errorf("API module %s DTOs must not depend on Echo", module)
		}
	}
	for _, module := range []string{
		"exchangeapi", "execapi", "fileapi", "fileopsapi", "mirrorapi", "previewapi",
	} {
		directory := filepath.Join(root, "internal", "controlplane", module)
		if _, err := os.Stat(filepath.Join(directory, "types.go")); err != nil {
			t.Errorf("API module %s must keep its types with the feature package: %v", module, err)
		}
		assertFileContentsDoNotContain(t, filepath.Join(directory, "types.go"), []string{"github.com/labstack/echo/v5"})
	}

	ticketDirectory := filepath.Join(root, "internal", "controlplane", "ticketapi")
	for _, name := range []string{"api.go", "dto.go", "service/service.go", "entity/ticket.go"} {
		path := filepath.Join(ticketDirectory, filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("layered ticket API must contain %s: %v", name, err)
		}
	}
	for _, name := range []string{"service.go", "model.go", "handler.go"} {
		path := filepath.Join(ticketDirectory, name)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("layered ticket API must not contain %s", name)
		}
	}
	for _, name := range []string{"service/service.go", "entity/ticket.go"} {
		path := filepath.Join(ticketDirectory, filepath.FromSlash(name))
		imports, err := fileImports(path)
		if err != nil {
			t.Errorf("read imports from %s: %v", path, err)
			continue
		}
		if slices.Contains(imports, "github.com/labstack/echo/v5") {
			t.Errorf("%s must not depend on Echo", path)
		}
	}

	portForwardDirectory := filepath.Join(root, "internal", "controlplane", "portforwardapi")
	for _, name := range []string{"api.go", "dto.go", "service/service.go"} {
		path := filepath.Join(portForwardDirectory, filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("layered Port Forward API must contain %s: %v", name, err)
		}
	}
	for _, name := range []string{"service.go", "model.go", "handler.go"} {
		path := filepath.Join(portForwardDirectory, name)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("layered Port Forward API must not contain %s", name)
		}
	}
	for _, name := range []string{"service/service.go"} {
		path := filepath.Join(portForwardDirectory, filepath.FromSlash(name))
		imports, err := fileImports(path)
		if err != nil {
			t.Errorf("read imports from %s: %v", path, err)
			continue
		}
		if slices.Contains(imports, "github.com/labstack/echo/v5") {
			t.Errorf("%s must not depend on Echo", path)
		}
	}

	httpAuthDirectory := filepath.Join(root, "internal", "controlplane", "authn", "httpauth")
	for _, name := range []string{"api.go", "dto.go", "factory.go", "service/service.go"} {
		path := filepath.Join(httpAuthDirectory, filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("layered HTTP auth API must contain %s: %v", name, err)
		}
	}
	for _, name := range []string{"service.go", "handler.go"} {
		path := filepath.Join(httpAuthDirectory, name)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("layered HTTP auth API must not contain %s", name)
		}
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(httpAuthDirectory, "service"), []string{"github.com/labstack/echo/v5"})
	for _, directory := range []string{ticketDirectory, portForwardDirectory, httpAuthDirectory} {
		for _, name := range []string{"endpoint.go", "routes.go"} {
			path := filepath.Join(directory, name)
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s must be merged into api.go", path)
			}
		}
	}

	sessionDirectory := filepath.Join(root, "internal", "controlplane", "sessionapi")
	for _, name := range []string{"routes.go", "service.go", "dto.go"} {
		path := filepath.Join(sessionDirectory, filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("session API must contain %s: %v", name, err)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "internal", "controlplane", "controlplaneapi", "types.go"),
		filepath.Join(root, "internal", "controlplane", "portforwardapi", "service", "types.go"),
		filepath.Join(root, "internal", "controlplane", "sessionapi", "types.go"),
		filepath.Join(root, "internal", "protocol", "trafficmodel", "model.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("controlplane type owner is missing %s: %v", path, err)
		}
		assertFileContentsDoNotContain(t, path, []string{"github.com/labstack/echo/v5"})
	}

	err := filepath.WalkDir(filepath.Join(root, "internal", "controlplane"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "contract.go" {
			t.Errorf("obsolete compatibility contract must remain removed: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk controlplane tree: %v", err)
	}

	for _, module := range []string{"exchangeapi", "fileapi", "mirrorapi", "portforwardapi"} {
		directory := filepath.Join(root, "internal", "controlplane", module)
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read controlplane module %s: %v", module, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.Contains(entry.Name(), "resolver") {
				t.Errorf("Kubernetes resolver implementation must stay in controlplane/kubernetes: %s", filepath.Join(directory, entry.Name()))
			}
		}
	}
}

func TestControlPlaneAPIMiddlewareStaysInMiddlewarePackage(t *testing.T) {
	root := repositoryRoot(t)
	apiPath := filepath.Join(root, "internal", "controlplane", "api.go")
	if _, err := os.Stat(apiPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mixed-responsibility internal/controlplane/api.go must remain removed: %v", err)
	}

	middlewarePath := filepath.Join(root, "internal", "controlplane", "middleware", "middleware.go")
	if _, err := os.Stat(middlewarePath); err != nil {
		t.Fatalf("controlplane API middleware package is missing: %v", err)
	}
	serverPath := filepath.Join(root, "internal", "controlplane", "server.go")
	assertFileContentsContain(t, serverPath, []string{
		"echomiddleware.RequestID()",
		"controlplanemiddleware.RequestLogger(logger)",
	})
	assertFileContentsDoNotContain(t, serverPath, []string{
		"type requestTracker struct", "newRequestTracker", "func requestLog(",
	})
	requestTrackerPath := filepath.Join(root, "internal", "controlplane", "middleware", "request_tracker.go")
	if _, err := os.Stat(requestTrackerPath); err != nil {
		t.Fatalf("controlplane request tracker middleware is missing: %v", err)
	}
	requestIDPath := filepath.Join(root, "internal", "controlplane", "middleware", "request_id.go")
	if _, err := os.Stat(requestIDPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("request ID must use Echo middleware directly; custom wrapper still exists: %v", err)
	}
	loggerPath := filepath.Join(root, "internal", "controlplane", "middleware", "logger.go")
	assertFileContentsContain(t, loggerPath, []string{"echomiddleware.RequestLoggerWithConfig"})
	assertFileContentsDoNotContain(t, middlewarePath, []string{"func newRequestID("})
	assertFileContentsDoNotContain(t, middlewarePath, []string{"type responseStateWriter struct"})
}

func TestControlPlaneAPIRoutesAreRegisteredCentrally(t *testing.T) {
	root := repositoryRoot(t)
	routesPath := filepath.Join(root, "internal", "controlplane", "routes.go")
	assertFileContentsContain(t, routesPath, []string{"/health/live", "/health/ready"})
	serverPath := filepath.Join(root, "internal", "controlplane", "server.go")
	assertFileContentsDoNotContain(t, serverPath, []string{"router.GET(\"/health/live\"", "router.GET(\"/health/ready\""})
	for _, module := range []string{
		"exchangeapi",
		"execapi",
		"fileapi",
		"fileopsapi",
		"kubeapi",
		"mirrorapi",
		"previewapi",
		"sessionapi",
	} {
		path := filepath.Join(root, "internal", "controlplane", module, "routes.go")
		assertFileContentsDoNotContain(t, path, []string{
			"RegisterRoutes(",
			"router.GET(",
			"router.POST(",
			"router.DELETE(",
		})
	}
}

func TestTrafficBindingOperatorExclusivelyOwnsKubernetesRecovery(t *testing.T) {
	root := repositoryRoot(t)
	for _, module := range []string{"exchangeapi", "mirrorapi", "previewapi"} {
		directory := filepath.Join(root, "internal", "controlplane", module)
		if _, err := os.Stat(filepath.Join(directory, "reconciler.go")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s must not duplicate TrafficBinding reconciliation", module)
		}
		if _, err := os.Stat(filepath.Join(directory, "stream.go")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s must not own a network stream", module)
		}
	}

	operatorReconciler := filepath.Join(root, "internal", "controller", "trafficbinding_controller.go")
	assertFileContentsContain(t, operatorReconciler, []string{
		"For(&trafficv1alpha1.TrafficBinding{})",
		"Owns(&corev1.Service{})",
		"Owns(&discoveryv1.EndpointSlice{})",
		"Watches(&corev1.Service{}",
		"Watches(&corev1.Endpoints{}",
		"controllerutil.AddFinalizer(binding, bindingFinalizer)",
		"r.cleanup(ctx, binding)",
	})

	controlPlaneMain := filepath.Join(root, "cmd", "kubeloop-control-plane", "main.go")
	assertFileContentsContain(t, controlPlaneMain, []string{"bindingRecovery, err := trafficbindingclient.NewReconciler("})
	assertFileContentsDoNotContain(t, controlPlaneMain, []string{"exchangeRecovery", "mirrorRecovery", "previewRecovery"})
}

func TestTrafficDataPlaneIsOwnedByGateway(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"api.go", "relay.go"} {
		path := filepath.Join(root, "internal", "gateway", "reverserelay", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("reverse relay infrastructure is missing %s: %v", path, err)
		}
	}
	trafficListeners := filepath.Join(root, "internal", "gateway", "trafficlistener", "listeners.go")
	if _, err := os.Stat(trafficListeners); err != nil {
		t.Errorf("Gateway traffic listener infrastructure is missing: %v", err)
	}
	mirrorRelay := filepath.Join(root, "internal", "gateway", "mirrorrelay", "shadow_relay.go")
	if _, err := os.Stat(mirrorRelay); err != nil {
		t.Errorf("Gateway Mirror relay is missing: %v", err)
	}
	for _, module := range []string{"exchangeapi", "mirrorapi", "previewapi"} {
		directory := filepath.Join(root, "internal", "controlplane", module)
		assertTreeImportsDoNotMatch(t, directory, []string{
			"github.com/coder/websocket",
			modulePath + "/internal/gateway",
			modulePath + "/internal/protocol/exchangestream",
			modulePath + "/internal/protocol/mirrorstream",
		})
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "gateway"), []string{modulePath + "/internal/controlplane"})
	assertFileContentsDoNotContain(t, filepath.Join(root, "internal", "controlplane", "routes.go"), []string{
		"/exchanges/:taskID/stream", "/mirrors/:taskID/stream", "/previews/:taskID/stream",
	})
	assertFileContentsContain(t, filepath.Join(root, "internal", "gateway", "trafficapi", "api.go"), []string{
		"trafficcontrol.PublicPathPrefix", "reverserelay.Run", "mirrorrelay.New",
	})
}

func TestControlPlaneAPIUsesEchoBindingAndResponses(t *testing.T) {
	root := repositoryRoot(t)
	assertFileContentsDoNotContain(t, filepath.Join(root, "internal", "controlplane", "options.go"), []string{
		"DecodeJSON", "json.NewDecoder",
	})

	for _, module := range []string{
		"exchangeapi",
		"execapi",
		"fileapi",
		"fileopsapi",
		"mirrorapi",
		"previewapi",
	} {
		path := filepath.Join(root, "internal", "controlplane", module, "service.go")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if !strings.Contains(string(raw), "ctx.Bind(") {
			t.Errorf("%s must bind request bodies through Echo", path)
		}
		assertFileContentsDoNotContain(t, path, []string{"json.NewEncoder(writer)", "DecodeJSON("})
	}
	ticketEndpoint := filepath.Join(root, "internal", "controlplane", "ticketapi", "api.go")
	assertFileContentsContain(t, ticketEndpoint, []string{"ctx.Bind(", "ctx.JSON("})
	assertFileContentsDoNotContain(t, ticketEndpoint, []string{"json.NewEncoder(writer)", "DecodeJSON("})
	portForwardEndpoint := filepath.Join(root, "internal", "controlplane", "portforwardapi", "api.go")
	assertFileContentsContain(t, portForwardEndpoint, []string{"ctx.Bind(", "ctx.JSON("})
	assertFileContentsDoNotContain(t, portForwardEndpoint, []string{"json.NewEncoder(writer)", "DecodeJSON("})
	httpAuthEndpoint := filepath.Join(root, "internal", "controlplane", "authn", "httpauth", "api.go")
	assertFileContentsContain(t, httpAuthEndpoint, []string{"ctx.Bind(", "ctx.JSON("})
	assertFileContentsDoNotContain(t, httpAuthEndpoint, []string{"json.NewEncoder(writer)", "DecodeJSON("})
}

func TestControlPlaneDoesNotImportDesktopOrDataPlaneRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/client",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		"github.com/wailsapp/",
	}
	root := repositoryRoot(t)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "internal", "controlplane"), forbidden)
	assertTreeImportsDoNotMatch(t, filepath.Join(root, "cmd", "kubeloop-control-plane"), forbidden)
}

func TestGatewayDoesNotImportControlPlaneOrDesktopRuntime(t *testing.T) {
	forbidden := []string{
		modulePath + "/internal/app",
		modulePath + "/internal/client",
		modulePath + "/internal/controlplane",
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
		modulePath + "/internal/controlplane",
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
		modulePath + "/internal/controlplane",
		modulePath + "/internal/gateway",
		modulePath + "/internal/helper",
		"k8s.io/",
	}
	assertTreeImportsDoNotMatch(t, filepath.Join(repositoryRoot(t), "internal", "mcp"), forbidden)
}

func TestTrafficWorkflowsDelegateKubernetesWritesToOperator(t *testing.T) {
	root := repositoryRoot(t)
	mainPath := filepath.Join(root, "cmd", "kubeloop-control-plane", "main.go")
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
		"api/v1alpha1",
		"cmd/kubeloop-control-plane",
		"cmd/kubeloop-operator",
		"internal/controller",
		"internal/controlplane/exchangeapi",
		"internal/controlplane/execapi",
		"internal/controlplane/fileapi",
		"internal/controlplane/fileopsapi",
		"internal/controlplane/kubeapi",
		"internal/controlplane/kubernetes",
		"internal/controlplane/mirrorapi",
		"internal/controlplane/networkapi",
		"internal/controlplane/portforwardapi",
		"internal/controlplane/portforwardapi/service",
		"internal/controlplane/previewapi",
		"internal/controlplane/relayregistry",
		"internal/controlplane/servicebinding",
		"internal/controlplane/sessionapi",
		"internal/controlplane/trafficbindingclient",
	}

	seen := make(map[string]struct{}, len(want))
	for _, tree := range []string{"api", "cmd", "internal"} {
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

func assertFileContentsContain(t *testing.T, path string, required []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range required {
		if !strings.Contains(string(raw), value) {
			t.Errorf("%s does not contain required implementation %q", path, value)
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
