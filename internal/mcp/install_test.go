package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestInstallClientConfigWithoutToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	url := "http://127.0.0.1:30808/mcp"
	if _, err := InstallClientConfig(ClientCursor, url, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cursor map[string]any
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	entry := cursor["mcpServers"].(map[string]any)["kubeloop"].(map[string]any)
	if entry["url"] != url {
		t.Fatalf("url=%v", entry["url"])
	}
	if _, ok := entry["headers"]; ok {
		t.Fatalf("headers should be omitted: %#v", entry["headers"])
	}
}

func TestInstallClientConfigCursorAndClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	url := "http://127.0.0.1:30808/mcp"
	token := "abc123"

	got, err := InstallClientConfig(ClientCursor, url, token)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, ".cursor", "mcp.json")
	if got.Path != wantPath {
		t.Fatalf("path=%q want %q", got.Path, wantPath)
	}
	raw, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	var cursor map[string]any
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	servers := cursor["mcpServers"].(map[string]any)
	entry := servers["kubeloop"].(map[string]any)
	if entry["url"] != url {
		t.Fatalf("url=%v", entry["url"])
	}
	if _, ok := entry["type"]; ok {
		t.Fatal("cursor entry should not force type")
	}

	// Preserve existing Claude keys while updating mcpServers.
	claudePath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudePath, []byte(`{"userID":"u1","mcpServers":{"other":{"type":"http","url":"https://example.com"}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallClientConfig(ClientClaude, url, token); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	var claude map[string]any
	if err := json.Unmarshal(raw, &claude); err != nil {
		t.Fatal(err)
	}
	if claude["userID"] != "u1" {
		t.Fatalf("lost userID: %#v", claude["userID"])
	}
	servers = claude["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("lost existing server")
	}
	entry = servers["kubeloop"].(map[string]any)
	if entry["type"] != "http" {
		t.Fatalf("claude type=%v", entry["type"])
	}
	headers := entry["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer abc123" {
		t.Fatalf("headers=%#v", headers)
	}
}

func TestInstallClientConfigVSCodeAndCodex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	url := "http://127.0.0.1:30808/mcp"
	token := "tok"

	if _, err := InstallClientConfig(ClientVSCode, url, token); err != nil {
		t.Fatal(err)
	}
	path, err := ClientConfigPath(ClientVSCode)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var vscode map[string]any
	if err := json.Unmarshal(raw, &vscode); err != nil {
		t.Fatal(err)
	}
	servers := vscode["servers"].(map[string]any)
	entry := servers["kubeloop"].(map[string]any)
	if entry["type"] != "http" || entry["url"] != url {
		t.Fatalf("vscode entry=%#v", entry)
	}

	codexPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `
# keep this comment
model = "gpt-5"

[mcp_servers.other]
command = "echo"

[mcp_servers."kubeloop"]
url = "http://old"

[mcp_servers."kubeloop".http_headers]
Authorization = "Bearer old"
`
	if err := os.WriteFile(codexPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallClientConfig(ClientCodex, url, token); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.Contains(text, "# keep this comment") ||
		!strings.Contains(text, `model = "gpt-5"`) ||
		!strings.Contains(text, `[mcp_servers.other]`) {
		t.Fatalf("lost unrelated config:\n%s", text)
	}
	if strings.Contains(text, "http://old") || strings.Contains(text, "Bearer old") {
		t.Fatalf("old kubeloop values remain:\n%s", text)
	}
	var codex map[string]any
	if err := toml.Unmarshal(updated, &codex); err != nil {
		t.Fatal(err)
	}
	codexServers := codex["mcp_servers"].(map[string]any)
	codexEntry := codexServers["kubeloop"].(map[string]any)
	if codexEntry["url"] != url {
		t.Fatalf("codex url = %#v", codexEntry["url"])
	}
	headers := codexEntry["http_headers"].(map[string]any)
	if headers["Authorization"] != "Bearer tok" {
		t.Fatalf("codex headers = %#v", headers)
	}
}

func TestInstallClientConfigRejectsUnknown(t *testing.T) {
	if _, err := InstallClientConfig("windsurf", "http://127.0.0.1:1/mcp", "t"); err == nil {
		t.Fatal("expected error")
	}
}

func TestInstallCodexWithoutTokenRemovesOldHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := `
[[profiles]]
name = "before"

[mcp_servers.kubeloop]
url = "http://old"

[mcp_servers.kubeloop.http_headers]
Authorization = "Bearer old"

[[profiles]]
name = "after"
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installCodexTOML(path, "http://127.0.0.1:30808/mcp", ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Bearer old") ||
		strings.Contains(string(raw), "http_headers") {
		t.Fatalf("stale authorization headers remain:\n%s", raw)
	}
	if strings.Count(string(raw), "[[profiles]]") != 2 ||
		!strings.Contains(string(raw), `name = "before"`) ||
		!strings.Contains(string(raw), `name = "after"`) {
		t.Fatalf("unrelated array tables were changed:\n%s", raw)
	}
	var decoded map[string]any
	if err := toml.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	server := decoded["mcp_servers"].(map[string]any)["kubeloop"].(map[string]any)
	if _, exists := server["http_headers"]; exists {
		t.Fatalf("http_headers should be absent: %#v", server)
	}
}

func TestInstallCodexInvalidTOMLLeavesFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := []byte("[mcp_servers.kubeloop\nurl = \"http://old\"\r\n")
	if err := os.WriteFile(path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installCodexTOML(path, "http://127.0.0.1:30808/mcp", "token"); err == nil {
		t.Fatal("expected invalid TOML error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, initial) {
		t.Fatalf("invalid config was modified:\ngot  %q\nwant %q", got, initial)
	}
}
