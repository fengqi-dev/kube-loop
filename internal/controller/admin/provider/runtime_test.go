package provider

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	adminrevision "github.com/fengqi-dev/kube-loop/internal/controller/admin/revision"
	authconfig "github.com/fengqi-dev/kube-loop/internal/controller/authn/config"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
)

type secretResolverStub map[string]string

func (resolver secretResolverStub) Resolve(alias, use string) (string, error) {
	value, ok := resolver[alias+":"+use]
	if !ok {
		return "", ErrUnavailable
	}
	return value, nil
}

func TestRuntimeUsesOnlyAllowlistedAliasesAndAtomicallyInstallsAggregate(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "provider.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	baseline := authconfig.File{DevelopmentMode: true, Providers: []authconfig.Provider{{
		ID: "managed", Type: "anonymous", Anonymous: &authconfig.AnonymousConfig{
			DevelopmentIdentityConfig: authconfig.DevelopmentIdentityConfig{Subject: "developer"},
		},
	}}}
	registry, err := authconfig.Build(context.Background(), baseline)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(store, registry, baseline, secretResolverStub{
		"corporate:client-secret": "/fixed/corporate/client-secret",
		"corporate:ca":            "/fixed/corporate/ca.crt",
	}, "https://gateway.example", 0)
	if err != nil {
		t.Fatal(err)
	}
	candidate := adminrevision.ProviderCandidate{
		ID: "oidc", Type: "oidc",
		Config:        json.RawMessage(`{"issuer":"https://issuer.example","clientId":"kubeloop","claims":{}}`),
		SecretAliases: json.RawMessage(`{"ca":"corporate","client-secret":"corporate"}`),
	}
	item, enabled, err := runtime.provider(candidate)
	if err != nil || !enabled || item.OIDC == nil ||
		item.OIDC.ClientSecretFile != "/fixed/corporate/client-secret" || item.OIDC.CAFile != "/fixed/corporate/ca.crt" {
		t.Fatalf("resolved Provider = %#v, enabled=%v, error=%v", item, enabled, err)
	}
	candidate.SecretAliases = json.RawMessage(`{"client-secret":"unknown"}`)
	if _, _, err := runtime.provider(candidate); err == nil {
		t.Fatal("unknown Secret alias was accepted")
	}

	disabled := adminrevision.ProviderCandidate{ID: "managed", Type: "oidc",
		Config:        json.RawMessage(`{"issuer":"https://issuer.example","clientId":"unused","claims":{},"enabled":false}`),
		SecretAliases: json.RawMessage(`{}`)}
	install, err := runtime.Prepare(context.Background(), disabled)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Descriptors()) != 1 {
		t.Fatal("Prepare mutated the live Registry")
	}
	install()
	if len(registry.Descriptors()) != 0 || runtime.Check(context.Background()) != nil {
		t.Fatalf("installed descriptors=%v readiness=%v", registry.Descriptors(), runtime.Check(context.Background()))
	}
}
