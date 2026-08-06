package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBackend struct {
	connected       contextNamespace
	commandRequest  PodCommandRequest
	transferRequest filemanager.TransferRequest
	cancelled       string
	manualNetwork   cluster.ManualNetwork
	hostAliases     []store.HostAliasSpec
	helperAction    string
	stoppedTraffic  map[string]string
	portForwards    []portfwd.Request
}

type contextNamespace struct {
	context   string
	namespace string
}

func (f *fakeBackend) SessionState() session.State {
	return session.State{Phase: session.PhaseIdle, Context: f.connected.context, Namespace: f.connected.namespace}
}
func (f *fakeBackend) ReloadContexts() (cluster.ClusterInventory, error) {
	return cluster.ClusterInventory{Contexts: []cluster.ContextInfo{{Name: "minikube"}}}, nil
}
func (f *fakeBackend) ProbeContext(context.Context, string) (cluster.ProbeResult, error) {
	return cluster.ProbeResult{OK: true, Version: "v1.30.0"}, nil
}
func (f *fakeBackend) Namespaces(context.Context, string) ([]string, error) {
	return []string{"default", "kube-system"}, nil
}
func (f *fakeBackend) ListServices(context.Context, string, string) ([]cluster.ServiceInfo, error) {
	return []cluster.ServiceInfo{{Name: "api", Namespace: "default"}}, nil
}
func (f *fakeBackend) ListPods(context.Context, string, string) ([]cluster.PodInfo, error) {
	return []cluster.PodInfo{{Name: "api-0", Namespace: "default"}}, nil
}
func (f *fakeBackend) Connect(_ context.Context, contextName, namespace string) error {
	f.connected = contextNamespace{context: contextName, namespace: namespace}
	return nil
}
func (f *fakeBackend) Disconnect() error {
	f.connected = contextNamespace{}
	return nil
}
func (f *fakeBackend) GetManualNetwork(string) cluster.ManualNetwork { return f.manualNetwork }
func (f *fakeBackend) SetManualNetwork(_ string, network cluster.ManualNetwork) error {
	f.manualNetwork = network
	return nil
}
func (f *fakeBackend) GetHostAliases(string) []store.HostAliasSpec { return f.hostAliases }
func (f *fakeBackend) SetHostAliases(_ string, items []store.HostAliasSpec) error {
	f.hostAliases = items
	return nil
}
func (f *fakeBackend) StartIntercept(context.Context, intercept.Mapping) (intercept.Info, error) {
	return intercept.Info{ID: "ex-1"}, nil
}
func (f *fakeBackend) StartMirror(context.Context, intercept.Mapping) (intercept.Info, error) {
	return intercept.Info{ID: "mi-1"}, nil
}
func (f *fakeBackend) StopIntercept(_ context.Context, id string) error {
	f.recordStoppedTraffic("intercept", id)
	return nil
}
func (f *fakeBackend) ListIntercepts() []intercept.Info {
	return []intercept.Info{{ID: "ex-1"}}
}
func (f *fakeBackend) ListMirrors() []intercept.Info {
	return []intercept.Info{{ID: "mi-1"}}
}
func (f *fakeBackend) StartPreview(context.Context, intercept.PreviewRequest) (intercept.Info, error) {
	return intercept.Info{ID: "pr-1", Preview: true}, nil
}
func (f *fakeBackend) StopPreview(_ context.Context, id string) error {
	f.recordStoppedTraffic("preview", id)
	return nil
}
func (f *fakeBackend) ListPreviews() []intercept.Info {
	return []intercept.Info{{ID: "pr-1", Preview: true}}
}
func (f *fakeBackend) StartPortForward(_ context.Context, request portfwd.Request) (portfwd.Info, error) {
	f.portForwards = append(f.portForwards, request)
	podName := request.Name
	if request.Kind == portfwd.KindService {
		podName = request.Name + "-pod"
	}
	return portfwd.Info{
		ID:         "pf-1",
		Context:    request.Context,
		Namespace:  request.Namespace,
		Kind:       request.Kind,
		Name:       request.Name,
		PodName:    podName,
		Protocol:   request.Protocol,
		RemotePort: request.RemotePort,
		LocalPort:  18080,
	}, nil
}
func (f *fakeBackend) StopPortForward(id string) error {
	f.recordStoppedTraffic("port_forward", id)
	return nil
}
func (f *fakeBackend) ListPortForwards() []portfwd.Info {
	return []portfwd.Info{{ID: "pf-1"}}
}
func (f *fakeBackend) HelperStatus(context.Context) helper.Status {
	return helper.Status{
		Installed: f.helperAction != "uninstall",
		Running:   f.helperAction != "uninstall",
		Expected:  "dev",
	}
}
func (f *fakeBackend) InstallHelper(context.Context) error {
	f.helperAction = "install"
	return nil
}
func (f *fakeBackend) UninstallHelper(context.Context) error {
	f.helperAction = "uninstall"
	return nil
}
func (f *fakeBackend) SingBoxConfig() ([]byte, error) {
	return []byte(`{"log":{"level":"info"}}`), nil
}
func (f *fakeBackend) ExecPodCommand(_ context.Context, request PodCommandRequest) (PodCommandResult, error) {
	f.commandRequest = request
	return PodCommandResult{
		Context: request.Context, Namespace: request.Namespace, Pod: request.Pod,
		Container: request.Container, Command: request.Command, Stdout: "hello\n",
	}, nil
}
func (f *fakeBackend) StartFileTransfer(
	_ context.Context,
	request filemanager.TransferRequest,
) (filemanager.TransferTask, error) {
	f.transferRequest = request
	return filemanager.TransferTask{
		ID: "transfer-1", Direction: request.Direction, Target: request.Target,
		SourcePath: request.SourcePath, Status: filemanager.StatusQueued,
	}, nil
}
func (f *fakeBackend) ListFileTransfers() []filemanager.TransferTask {
	return []filemanager.TransferTask{{ID: "transfer-1", Status: filemanager.StatusRunning}}
}
func (f *fakeBackend) CancelFileTransfer(id string) error {
	f.cancelled = id
	return nil
}

func (f *fakeBackend) recordStoppedTraffic(kind, id string) {
	if f.stoppedTraffic == nil {
		f.stoppedTraffic = make(map[string]string)
	}
	f.stoppedTraffic[kind] = id
}

func TestManageCluster(t *testing.T) {
	backend := &fakeBackend{}
	tests := []struct {
		name  string
		in    manageClusterIn
		check func(t *testing.T, out manageClusterOut)
	}{
		{
			name: "reload contexts",
			in:   manageClusterIn{Action: "reload", Type: "context"},
			check: func(t *testing.T, out manageClusterOut) {
				if out.Inventory == nil || len(out.Inventory.Contexts) != 1 {
					t.Fatalf("inventory=%#v", out.Inventory)
				}
			},
		},
		{
			name: "probe context",
			in:   manageClusterIn{Action: "probe", Type: "context", Context: "minikube"},
			check: func(t *testing.T, out manageClusterOut) {
				if out.Probe == nil || !out.Probe.OK {
					t.Fatalf("probe=%#v", out.Probe)
				}
			},
		},
		{
			name: "list namespaces",
			in:   manageClusterIn{Action: "list", Type: "namespace", Context: "minikube"},
			check: func(t *testing.T, out manageClusterOut) {
				if len(out.Namespaces) != 2 {
					t.Fatalf("namespaces=%#v", out.Namespaces)
				}
			},
		},
		{
			name: "list services",
			in: manageClusterIn{
				Action: "list", Type: "service", Context: "minikube", Namespace: "default",
			},
			check: func(t *testing.T, out manageClusterOut) {
				if len(out.Services) != 1 || out.Services[0].Name != "api" {
					t.Fatalf("services=%#v", out.Services)
				}
			},
		},
		{
			name: "list pods",
			in: manageClusterIn{
				Action: "list", Type: "pod", Context: "minikube", Namespace: "default",
			},
			check: func(t *testing.T, out manageClusterOut) {
				if len(out.Pods) != 1 || out.Pods[0].Name != "api-0" {
					t.Fatalf("pods=%#v", out.Pods)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, err := manageCluster(context.Background(), backend, test.in)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, out)
		})
	}
	if _, err := manageCluster(
		context.Background(),
		backend,
		manageClusterIn{Action: "list", Type: "service", Context: "minikube"},
	); err == nil {
		t.Fatal("expected missing namespace error")
	}
}

func TestManageConnectionAndFileTransfer(t *testing.T) {
	backend := &fakeBackend{}
	connection, err := manageConnection(
		context.Background(),
		backend,
		manageConnectionIn{Action: "connect", Context: "minikube", Namespace: "default"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if connection.State == nil || connection.State.Context != "minikube" {
		t.Fatalf("connection=%#v", connection)
	}
	config, err := manageConnection(
		context.Background(),
		backend,
		manageConnectionIn{Action: "config"},
	)
	if err != nil {
		t.Fatal(err)
	}
	logConfig, ok := config.Config["log"].(map[string]any)
	if !ok || logConfig["level"] != "info" {
		t.Fatalf("config=%#v", config.Config)
	}
	if _, err := manageConnection(
		context.Background(),
		backend,
		manageConnectionIn{Action: "disconnect"},
	); err != nil {
		t.Fatal(err)
	}

	transfers, err := manageFileTransfer(
		context.Background(),
		backend,
		manageFileTransferIn{Action: "list"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfers.Items) != 1 || transfers.Items[0].ID != "transfer-1" {
		t.Fatalf("transfers=%#v", transfers.Items)
	}
	if _, err := manageFileTransfer(
		context.Background(),
		backend,
		manageFileTransferIn{Action: "cancel"},
	); err == nil {
		t.Fatal("expected missing transfer id error")
	}
}

func TestManageTrafficListAllAndStop(t *testing.T) {
	backend := &fakeBackend{}
	out, err := manageTraffic(context.Background(), backend, manageTrafficIn{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 4 {
		t.Fatalf("items=%#v", out.Items)
	}
	for _, test := range []struct {
		trafficType string
		id          string
		stoppedAs   string
	}{
		{trafficType: "exchange", id: "ex-1", stoppedAs: "intercept"},
		{trafficType: "mirror", id: "mi-1", stoppedAs: "intercept"},
		{trafficType: "preview", id: "pr-1", stoppedAs: "preview"},
		{trafficType: "port_forward", id: "pf-1", stoppedAs: "port_forward"},
	} {
		_, err := manageTraffic(context.Background(), backend, manageTrafficIn{
			Action: "stop", Type: test.trafficType, ID: test.id,
		})
		if err != nil {
			t.Fatalf("stop %s: %v", test.trafficType, err)
		}
		if backend.stoppedTraffic[test.stoppedAs] != test.id {
			t.Fatalf("stopped=%#v", backend.stoppedTraffic)
		}
	}
}

func TestManageTrafficPortForwardTargetKind(t *testing.T) {
	backend := &fakeBackend{}
	for _, test := range []struct {
		kind        string
		name        string
		wantPodName string
	}{
		{kind: portfwd.KindPod, name: "api-0", wantPodName: "api-0"},
		{kind: portfwd.KindService, name: "api", wantPodName: "api-pod"},
	} {
		out, err := manageTraffic(context.Background(), backend, manageTrafficIn{
			Action:     "start",
			Type:       "port_forward",
			Context:    "minikube",
			Namespace:  "default",
			TargetKind: test.kind,
			TargetName: test.name,
			RemotePort: 8080,
		})
		if err != nil {
			t.Fatalf("start %s port-forward: %v", test.kind, err)
		}
		info := out.Item.PortForward
		if info.Kind != test.kind || info.Name != test.name || info.PodName != test.wantPodName {
			t.Fatalf("port-forward info=%#v", info)
		}
		request := backend.portForwards[len(backend.portForwards)-1]
		if request.Kind != test.kind || request.Name != test.name {
			t.Fatalf("port-forward request=%#v", request)
		}
	}

	callCount := len(backend.portForwards)
	for _, test := range []struct {
		name       string
		targetKind string
		targetName string
		wantError  string
	}{
		{
			name:       "missing target kind",
			targetName: "api-0",
			wantError:  "targetKind must be",
		},
		{
			name:       "invalid target kind",
			targetKind: "deployment",
			targetName: "api",
			wantError:  "targetKind must be",
		},
		{
			name:       "missing target name",
			targetKind: portfwd.KindService,
			wantError:  "targetName is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := manageTraffic(context.Background(), backend, manageTrafficIn{
				Action:     "start",
				Type:       "port_forward",
				TargetKind: test.targetKind,
				TargetName: test.targetName,
				RemotePort: 8080,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v, want substring %q", err, test.wantError)
			}
		})
	}
	if len(backend.portForwards) != callCount {
		t.Fatalf("invalid requests reached backend: %#v", backend.portForwards[callCount:])
	}
}

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token length=%d", len(token))
	}
}

func TestServerRejectsMissingBearer(t *testing.T) {
	backend := &fakeBackend{}
	srv := NewServer(backend, "test")
	port := freePort(t)
	srv.Configure(store.MCPConfig{Enabled: true, Port: port, TokenEnabled: true, Token: "secret-token"})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop() }()

	status := srv.Status()
	if !status.Listening || status.URL == "" || !status.TokenEnabled {
		t.Fatalf("status=%#v", status)
	}

	req, err := http.NewRequest(http.MethodPost, status.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestServerAllowsMissingBearerWhenTokenDisabled(t *testing.T) {
	backend := &fakeBackend{}
	srv := NewServer(backend, "test")
	port := freePort(t)
	srv.Configure(store.MCPConfig{Enabled: true, Port: port, TokenEnabled: false})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop() }()

	status := srv.Status()
	if !status.Listening || status.TokenEnabled || status.Token != "" {
		t.Fatalf("status=%#v", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: status.URL}, nil)
	if err != nil {
		t.Fatalf("connect without bearer: %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected tools")
	}
}

func TestServerToolsConnectAndList(t *testing.T) {
	backend := &fakeBackend{}
	srv := NewServer(backend, "test")
	token := "test-bearer-token"
	port := freePort(t)
	srv.Configure(store.MCPConfig{Enabled: true, Port: port, TokenEnabled: true, Token: token})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop() }()

	endpoint := srv.Status().URL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: &bearerRoundTripper{token: token, base: http.DefaultTransport},
		},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectedTools := map[string]bool{
		"manage_cluster":       false,
		"manage_connection":    false,
		"manage_network":       false,
		"manage_traffic":       false,
		"manage_helper":        false,
		"exec_pod_command":     false,
		"manage_file_transfer": false,
	}
	if len(tools.Tools) != len(expectedTools) {
		t.Fatalf("expected %d tools, got %d", len(expectedTools), len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if _, ok := expectedTools[tool.Name]; !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		expectedTools[tool.Name] = true
	}
	for name, found := range expectedTools {
		if !found {
			t.Fatalf("missing tool %q", name)
		}
	}

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "manage_connection",
		Arguments: map[string]any{
			"action": "connect", "context": "minikube", "namespace": "default",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %#v", res)
	}
	if backend.connected.context != "minikube" || backend.connected.namespace != "default" {
		t.Fatalf("connected=%#v", backend.connected)
	}

	listRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "manage_cluster",
		Arguments: map[string]any{
			"action": "list", "type": "namespace", "context": "minikube",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if listRes.IsError {
		t.Fatalf("list error: %#v", listRes)
	}
	raw, _ := json.Marshal(listRes.StructuredContent)
	if !strings.Contains(string(raw), "default") {
		t.Fatalf("namespaces=%s", raw)
	}

	commandRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "exec_pod_command",
		Arguments: map[string]any{
			"context": "minikube", "namespace": "default", "pod": "api-0",
			"container": "api", "command": "printf hello",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commandRes.IsError {
		t.Fatalf("command error: %#v", commandRes)
	}
	if backend.commandRequest.Command != "printf hello" || backend.commandRequest.Container != "api" {
		t.Fatalf("command request=%#v", backend.commandRequest)
	}
	raw, _ = json.Marshal(commandRes.StructuredContent)
	if !strings.Contains(string(raw), `"stdout":"hello\n"`) {
		t.Fatalf("command result=%s", raw)
	}

	transferRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "manage_file_transfer",
		Arguments: map[string]any{
			"action": "start", "direction": "upload", "context": "minikube", "namespace": "default",
			"pod": "api-0", "container": "api", "sourcePath": "/tmp/input.txt",
			"destinationDir": "/tmp",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transferRes.IsError {
		t.Fatalf("transfer error: %#v", transferRes)
	}
	if backend.transferRequest.Direction != filemanager.DirectionUpload ||
		backend.transferRequest.Target.Pod != "api-0" {
		t.Fatalf("transfer request=%#v", backend.transferRequest)
	}

	cancelRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "manage_file_transfer",
		Arguments: map[string]any{"action": "cancel", "id": "transfer-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelRes.IsError || backend.cancelled != "transfer-1" {
		t.Fatalf("cancel result=%#v cancelled=%q", cancelRes, backend.cancelled)
	}

	networkRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "manage_network",
		Arguments: map[string]any{
			"action": "set", "type": "host_aliases", "context": "minikube",
			"items": []any{map[string]any{"domain": "api.local", "ip": "10.0.0.8"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if networkRes.IsError || len(backend.hostAliases) != 1 ||
		backend.hostAliases[0].Domain != "api.local" {
		t.Fatalf("network result=%#v aliases=%#v", networkRes, backend.hostAliases)
	}

	helperRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "manage_helper",
		Arguments: map[string]any{"action": "uninstall"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if helperRes.IsError || backend.helperAction != "uninstall" {
		t.Fatalf("helper result=%#v action=%q", helperRes, backend.helperAction)
	}

	trafficRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "manage_traffic",
		Arguments: map[string]any{
			"action": "start", "type": "mirror", "namespace": "default",
			"service": "api",
			"ports": []any{map[string]any{
				"servicePort": 8080, "localPort": 3000,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if trafficRes.IsError {
		t.Fatalf("traffic result=%#v", trafficRes)
	}
	raw, _ = json.Marshal(trafficRes.StructuredContent)
	if !strings.Contains(string(raw), `"type":"mirror"`) ||
		!strings.Contains(string(raw), `"id":"mi-1"`) {
		t.Fatalf("traffic result=%s", raw)
	}
}

func TestServerStopWithHangingGET(t *testing.T) {
	backend := &fakeBackend{}
	srv := NewServer(backend, "test")
	port := freePort(t)
	srv.Configure(store.MCPConfig{Enabled: true, Port: port, TokenEnabled: false})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: srv.Status().URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	done := make(chan error, 1)
	go func() { done <- srv.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop with hanging GET: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on hanging Streamable HTTP GET")
	}
}

func TestServerStartIdempotentKeepsSession(t *testing.T) {
	backend := &fakeBackend{}
	srv := NewServer(backend, "test")
	port := freePort(t)
	srv.Configure(store.MCPConfig{Enabled: true, Port: port, TokenEnabled: false})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: srv.Status().URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatalf("list tools after redundant Start: %v", err)
	}
}

func TestServerRejectsWrongBearer(t *testing.T) {
	backend := &fakeBackend{}
	srv := NewServer(backend, "test")
	port := freePort(t)
	srv.Configure(store.MCPConfig{Enabled: true, Port: port, TokenEnabled: true, Token: "correct"})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Stop() }()

	req, err := http.NewRequest(http.MethodPost, srv.Status().URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
