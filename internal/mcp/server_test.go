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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	server := NewServer(&fakeBackend{}, "test")
	server.Configure(Config{
		Enabled: true, Port: freePort(t), TokenEnabled: true, Token: strings.Repeat("s", 64),
	})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	request, err := http.NewRequest(
		http.MethodPost,
		server.Status().URL,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}

func TestServerRestartsWhenTokenSettingsChange(t *testing.T) {
	server := NewServer(&fakeBackend{}, "test")
	server.Configure(Config{Enabled: true, Port: freePort(t)})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()
	if err := server.SetToken(""); err == nil {
		t.Fatal("empty MCP token was accepted")
	}
	firstToken := strings.Repeat("a", 64)
	if err := server.SetToken(firstToken); err != nil {
		t.Fatal(err)
	}
	if err := server.SetTokenEnabled(true); err != nil {
		t.Fatal(err)
	}
	status := server.Status()
	if !status.Listening || !status.TokenEnabled || status.Token != firstToken {
		t.Fatalf("token-enabled MCP status = %#v", status)
	}
	if got := mcpRequestStatus(t, status.URL, ""); got != http.StatusUnauthorized {
		t.Fatalf("missing-token status = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := mcpRequestStatus(t, status.URL, firstToken); got == http.StatusUnauthorized {
		t.Fatal("configured MCP token was rejected")
	}

	secondToken := strings.Repeat("b", 64)
	if err := server.SetToken(secondToken); err != nil {
		t.Fatal(err)
	}
	if got := mcpRequestStatus(t, status.URL, firstToken); got != http.StatusUnauthorized {
		t.Fatalf("old-token status = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := mcpRequestStatus(t, status.URL, secondToken); got == http.StatusUnauthorized {
		t.Fatal("replacement MCP token was rejected")
	}
	if err := server.SetTokenEnabled(false); err != nil {
		t.Fatal(err)
	}
	status = server.Status()
	if !status.Listening || status.TokenEnabled || status.Token != "" {
		t.Fatalf("token-disabled MCP status = %#v", status)
	}
	if got := mcpRequestStatus(t, status.URL, ""); got == http.StatusUnauthorized {
		t.Fatal("token-disabled MCP server required authorization")
	}
}

func TestServerPublishesAndExecutesV2Tools(t *testing.T) {
	backend := &fakeBackend{}
	server := NewServer(backend, "test")
	token := strings.Repeat("t", 64)
	server.Configure(Config{Enabled: true, Port: freePort(t), TokenEnabled: true, Token: token})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   server.Status().URL,
		HTTPClient: &http.Client{Transport: &bearerRoundTripper{token: token, base: http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"manage_cluster": false, "manage_connection": false, "manage_traffic": false,
		"exec_pod_command": false, "manage_file_transfer": false, "manage_pod_files": false,
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("tools=%#v", listed.Tools)
	}
	for _, tool := range listed.Tools {
		if _, found := want[tool.Name]; !found {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		want[tool.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing tool %q", name)
		}
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "manage_connection",
		Arguments: map[string]any{"action": "connect", "profileId": "server-a", "namespace": "default"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || backend.connectedProfile != "server-a" || backend.connectedNamespace != "default" {
		raw, _ := json.Marshal(result)
		t.Fatalf("result=%s backend=%#v", raw, backend)
	}

	calls := []mcpsdk.CallToolParams{
		{Name: "manage_cluster", Arguments: map[string]any{
			"action": "list", "type": "pod", "profileId": "server-a", "namespace": "default",
		}},
		{Name: "manage_connection", Arguments: map[string]any{
			"action": "status", "profileId": "server-a",
		}},
		{Name: "manage_traffic", Arguments: map[string]any{
			"action": "start", "type": "port_forward", "profileId": "server-a",
			"sessionId": "session-1", "namespace": "default", "targetKind": "pod",
			"targetName": "api-0", "protocol": "tcp", "remotePort": 8080,
		}},
		{Name: "exec_pod_command", Arguments: map[string]any{
			"profileId": "server-a", "sessionId": "session-1", "namespace": "default",
			"pod": "api-0", "command": []string{"printf", "ok"},
		}},
		{Name: "manage_file_transfer", Arguments: map[string]any{
			"action": "start", "profileId": "server-a", "sessionId": "session-1", "namespace": "default",
			"direction": "upload", "kind": "file", "pod": "api-0",
			"localPath": "/tmp/input", "remotePath": "/tmp/output", "overwrite": true,
		}},
		{Name: "manage_pod_files", Arguments: map[string]any{
			"action": "list", "profileId": "server-a", "sessionId": "session-1", "namespace": "default",
			"pod": "api-0", "container": "api", "path": "/tmp",
		}},
		{Name: "manage_pod_files", Arguments: map[string]any{
			"action": "create", "profileId": "server-a", "sessionId": "session-1", "namespace": "default",
			"pod": "api-0", "container": "api", "path": "/tmp/work", "kind": "directory",
			"idempotencyKey": "server-http-create-work",
		}},
	}
	for _, call := range calls {
		called, callErr := session.CallTool(ctx, &call)
		if callErr != nil || called.IsError {
			raw, _ := json.Marshal(called)
			t.Fatalf("call %s: result=%s error=%v", call.Name, raw, callErr)
		}
	}
}

func TestServerToolErrorsAreStableJSON(t *testing.T) {
	server := NewServer(&fakeBackend{}, "test")
	server.Configure(Config{Enabled: true, Port: freePort(t), TokenEnabled: false})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: server.Status().URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "manage_connection", Arguments: map[string]any{"action": "disconnect", "profileId": "server-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) == 0 {
		t.Fatalf("result=%#v", result)
	}
	raw, err := json.Marshal(result.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `\"code\":\"invalid_argument\"`) ||
		!strings.Contains(string(raw), `\"field\":\"sessionId\"`) {
		t.Fatalf("error content=%s", raw)
	}
}

func TestLocalOriginRequiresExactHostname(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:3000", "https://127.0.0.1", "http://[::1]:4000", "null",
	} {
		if !isLocalOrigin(origin) {
			t.Fatalf("local origin rejected: %q", origin)
		}
	}
	for _, origin := range []string{
		"http://localhost.evil.example", "https://127.0.0.1.evil", "https://example.com", "file://localhost/tmp",
	} {
		if isLocalOrigin(origin) {
			t.Fatalf("non-local origin accepted: %q", origin)
		}
	}
}

func TestServerRejectsNonLocalOrigin(t *testing.T) {
	server := NewServer(&fakeBackend{}, "test")
	server.Configure(Config{Enabled: true, Port: freePort(t), TokenEnabled: false})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()
	request, err := http.NewRequest(http.MethodPost, server.Status().URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://localhost.evil.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestServerStopWithStreamableHTTPSession(t *testing.T) {
	server := NewServer(&fakeBackend{}, "test")
	server.Configure(Config{Enabled: true, Port: freePort(t), TokenEnabled: false})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: server.Status().URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	done := make(chan error, 1)
	go func() { done <- server.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on Streamable HTTP session")
	}
}

func TestServerStopWaitsForServeCleanup(t *testing.T) {
	server := NewServer(&fakeBackend{}, "test")
	server.Configure(Config{Enabled: true, Port: freePort(t), TokenEnabled: false})
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	server.SetErrorHandler(func(error) {
		close(cleanupStarted)
		<-releaseCleanup
	})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}

	server.mu.Lock()
	listener := server.listener
	server.mu.Unlock()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleanupStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve cleanup did not start")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- server.Stop() }()
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before Serve cleanup completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCleanup)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not wait for Serve cleanup")
	}
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (transport *bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func mcpRequestStatus(t *testing.T, endpoint, token string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode
}
