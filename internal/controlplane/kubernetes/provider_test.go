package kubernetes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
)

func TestServiceAccountModeDoesNotImpersonateOrMutateBase(t *testing.T) {
	base := &rest.Config{Host: "https://kubernetes.example.test", UserAgent: "original", QPS: 1, Burst: 2}
	provider, err := NewForRESTConfig(base, Config{Timeout: 3 * time.Second, QPS: 7, Burst: 9, UserAgent: "kubeloop/test"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := provider.RESTConfigFor(authorization.Subject{ID: "alice", Groups: []string{"admins"}})
	if err != nil {
		t.Fatal(err)
	}
	if config.Impersonate.UserName != "" || len(config.Impersonate.Groups) != 0 {
		t.Fatalf("ServiceAccount mode unexpectedly impersonates: %+v", config.Impersonate)
	}
	if config.Timeout != 3*time.Second || config.QPS != 7 || config.Burst != 9 || config.UserAgent != "kubeloop/test" {
		t.Fatalf("unexpected REST defaults: %+v", config)
	}
	if config.ContentType != kruntime.ContentTypeJSON || config.AcceptContentTypes != kruntime.ContentTypeJSON {
		t.Fatalf("unexpected Kubernetes media types: %q %q", config.ContentType, config.AcceptContentTypes)
	}
	if base.UserAgent != "original" || base.QPS != 1 || base.Burst != 2 || base.Timeout != 0 {
		t.Fatalf("base configuration was mutated: %+v", base)
	}
}

func TestImpersonationUsesOnlyExplicitGroupMappings(t *testing.T) {
	provider, err := NewForRESTConfig(&rest.Config{Host: "https://kubernetes.example.test"}, Config{
		Impersonation: ImpersonationConfig{
			Enabled: true, UsernamePrefix: "kubeloop:user:",
			GroupMappings: map[string][]string{
				"developers": {"team:dev", "shared"},
				"operators":  {"shared", "team:ops"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := provider.RESTConfigFor(authorization.Subject{
		ID: "identity-123", Groups: []string{"developers", "unmapped", "operators"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Impersonate.UserName != "kubeloop:user:identity-123" {
		t.Fatalf("unexpected impersonated user %q", config.Impersonate.UserName)
	}
	want := []string{"shared", "team:dev", "team:ops"}
	if !slices.Equal(config.Impersonate.Groups, want) {
		t.Fatalf("unexpected impersonated groups: got %v want %v", config.Impersonate.Groups, want)
	}
}

func TestImpersonatingClientPreservesGatewayCredentialAndSendsMappedIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/version" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer gateway-service-account-token" {
			t.Fatalf("Gateway credential = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Impersonate-User") != "kubeloop:identity-123" {
			t.Fatalf("Impersonate-User = %q", request.Header.Get("Impersonate-User"))
		}
		groups := request.Header.Values("Impersonate-Group")
		if !slices.Equal(groups, []string{"kubeloop:developers"}) || slices.Contains(groups, "unmapped-claim") {
			t.Fatalf("Impersonate-Group = %v", groups)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.35.0"}`))
	}))
	defer server.Close()
	provider, err := NewForRESTConfig(&rest.Config{
		Host: server.URL, BearerToken: "gateway-service-account-token",
	}, Config{Impersonation: ImpersonationConfig{
		Enabled: true, UsernamePrefix: "kubeloop:",
		GroupMappings: map[string][]string{"trusted-claim": {"kubeloop:developers"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := provider.ClientFor(authorization.Subject{
		ID: "identity-123", Groups: []string{"trusted-claim", "unmapped-claim"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Discovery().ServerVersion(); err != nil {
		t.Fatal(err)
	}
}

func TestImpersonationRejectsUnsafeConfigurationAndIdentity(t *testing.T) {
	for _, config := range []Config{
		{Impersonation: ImpersonationConfig{Enabled: true, UsernamePrefix: "system:masters:"}},
		{Impersonation: ImpersonationConfig{Enabled: true, GroupMappings: map[string][]string{"bad\n": {"dev"}}}},
		{Impersonation: ImpersonationConfig{Enabled: true, GroupMappings: map[string][]string{"dev": {}}}},
	} {
		if _, err := NewForRESTConfig(&rest.Config{Host: "https://kubernetes.example.test"}, config); err == nil {
			t.Fatalf("expected invalid configuration to fail: %+v", config)
		}
	}
	provider, err := NewForRESTConfig(&rest.Config{Host: "https://kubernetes.example.test"}, Config{
		Impersonation: ImpersonationConfig{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RESTConfigFor(authorization.Subject{ID: "bad\nidentity"}); err == nil {
		t.Fatal("expected unsafe identity to fail")
	}
}

func TestCheckUsesRequestContext(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
	}))
	defer server.Close()
	provider, err := NewForRESTConfig(&rest.Config{Host: server.URL}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- provider.Check(ctx) }()
	<-requestStarted
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected canceled readiness check to fail")
		}
	case <-time.After(time.Second):
		t.Fatal("readiness check ignored cancellation")
	}
}

func TestCheckVersionEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/version" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.30.0"}`))
	}))
	defer server.Close()
	provider, err := NewForRESTConfig(&rest.Config{Host: server.URL}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLoadStrictConfiguration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "kubernetes.json")
	contents := `{"timeout":"4s","qps":12,"burst":18,"userAgent":"kubeloop/test","impersonation":{"enabled":true,"usernamePrefix":"gateway:","groupMappings":{"engineering":["k8s:dev"]}}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Timeout != 4*time.Second || config.QPS != 12 || config.Burst != 18 || !config.Impersonation.Enabled {
		t.Fatalf("unexpected configuration: %+v", config)
	}
	if err := os.WriteFile(path, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field to fail")
	}
}
