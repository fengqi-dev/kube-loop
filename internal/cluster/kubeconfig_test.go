package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeKubeconfig(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func TestContextsMergeAndSource(t *testing.T) {
	dir := t.TempDir()
	first := writeKubeconfig(t, dir, "first.yaml", `
apiVersion: v1
kind: Config
current-context: alpha
contexts:
- name: alpha
  context:
    cluster: cluster-a
    user: user-a
    namespace: ns-a
- name: shared
  context:
    cluster: cluster-a
    user: user-a
clusters:
- name: cluster-a
  cluster:
    server: https://a.example
users:
- name: user-a
  user:
    token: token-a
`)
	second := writeKubeconfig(t, dir, "second.yaml", `
apiVersion: v1
kind: Config
current-context: beta
contexts:
- name: beta
  context:
    cluster: cluster-b
    user: user-b
- name: shared
  context:
    cluster: cluster-b
    user: user-b
clusters:
- name: cluster-b
  cluster:
    server: https://b.example
users:
- name: user-b
  user:
    token: token-b
`)

	provider := NewProvider()
	t.Setenv("KUBECONFIG", first)
	provider.SetExtraKubeconfigFiles([]string{second})

	items, err := provider.Contexts()
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	byName := map[string]ContextInfo{}
	for _, item := range items {
		byName[item.Name] = item
	}
	if len(byName) != 3 {
		t.Fatalf("expected 3 contexts, got %d", len(byName))
	}
	shared := byName["shared"]
	if shared.Server != "https://b.example" {
		t.Fatalf("shared server = %q, want later file", shared.Server)
	}
	if shared.Source != second {
		t.Fatalf("shared source = %q, want %q", shared.Source, second)
	}
	if shared.User != "user-b" {
		t.Fatalf("shared user = %q", shared.User)
	}
	alpha := byName["alpha"]
	if alpha.Namespace != "ns-a" || alpha.Source != first {
		t.Fatalf("alpha = %+v", alpha)
	}
	if !byName["beta"].Current {
		t.Fatalf("expected beta current from later file")
	}

	files := provider.KubeconfigFiles()
	if len(files) < 2 {
		t.Fatalf("expected default + extra files, got %+v", files)
	}
	var sawExtra bool
	for _, file := range files {
		if file.Path == second && !file.Default {
			sawExtra = true
		}
	}
	if !sawExtra {
		t.Fatalf("extra file missing: %+v", files)
	}
}

func TestValidateKubeconfigFile(t *testing.T) {
	dir := t.TempDir()
	good := writeKubeconfig(t, dir, "good.yaml", `
apiVersion: v1
kind: Config
contexts:
- name: demo
  context:
    cluster: c
    user: u
clusters:
- name: c
  cluster:
    server: https://example
users:
- name: u
  user:
    token: t
`)
	if err := ValidateKubeconfigFile(good); err != nil {
		t.Fatalf("ValidateKubeconfigFile: %v", err)
	}
	empty := writeKubeconfig(t, dir, "empty.yaml", `
apiVersion: v1
kind: Config
contexts: []
clusters: []
users: []
`)
	if err := ValidateKubeconfigFile(empty); err == nil {
		t.Fatal("expected error for empty contexts")
	}
}

func TestValidateKubeconfigContent(t *testing.T) {
	valid := []byte(`apiVersion: v1
kind: Config
contexts:
- name: pasted
  context:
    cluster: demo
clusters:
- name: demo
  cluster:
    server: https://example.test
`)
	if err := ValidateKubeconfigContent(valid); err != nil {
		t.Fatalf("ValidateKubeconfigContent: %v", err)
	}
	if err := ValidateKubeconfigContent([]byte("apiVersion: v1\nkind: Config\n")); err == nil {
		t.Fatal("expected error for content without contexts")
	}
	if err := ValidateKubeconfigContent([]byte("not: [valid")); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestContextsWithoutReadableFilesReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, "missing.yaml"))

	items, err := NewProvider().Contexts()
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Contexts = %+v, want empty", items)
	}
}

func TestContextsStillReportsInvalidKubeconfig(t *testing.T) {
	dir := t.TempDir()
	invalid := writeKubeconfig(t, dir, "invalid.yaml", "not: [valid")
	t.Setenv("KUBECONFIG", invalid)

	if _, err := NewProvider().Contexts(); err == nil {
		t.Fatal("expected invalid kubeconfig error")
	}
}

func TestProbeSuccessAndTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"major":"1","minor":"29","gitVersion":"v1.29.0"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	path := writeKubeconfig(t, dir, "probe.yaml", `
apiVersion: v1
kind: Config
current-context: demo
contexts:
- name: demo
  context:
    cluster: c
    user: u
clusters:
- name: c
  cluster:
    server: `+server.URL+`
    insecure-skip-tls-verify: true
users:
- name: u
  user:
    token: t
`)
	provider := NewProvider()
	t.Setenv("KUBECONFIG", path)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := provider.Probe(ctx, "demo")
	if !result.OK {
		t.Fatalf("probe failed: %+v", result)
	}
	if result.Version != "v1.29.0" {
		t.Fatalf("version = %q", result.Version)
	}

	missing := provider.Probe(ctx, "")
	if missing.OK || missing.Error == "" {
		t.Fatalf("expected empty context error, got %+v", missing)
	}
}
