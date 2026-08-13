package provider

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	authconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/config"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestRuntimeUsesDatabaseCredentialsAndAtomicallyInstallsAggregate(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "provider.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	baseline := authconfig.File{}
	registry, err := authconfig.Build(context.Background(), baseline)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(store, registry, "https://gateway.example", 0)
	if err != nil {
		t.Fatal(err)
	}
	candidate := adminrevision.ProviderCandidate{
		ID: "oidc", Type: "oidc",
		Config: json.RawMessage(`{"issuer":"https://issuer.example","clientId":"kubeloop","clientSecret":"database-secret","caPem":"database-ca","claims":{}}`),
	}
	item, enabled, err := runtime.provider(candidate)
	if err != nil || !enabled || item.OIDC == nil ||
		item.OIDC.ClientSecret != "database-secret" || item.OIDC.CAPEM != "database-ca" || item.OIDC.ClientSecretFile != "" {
		t.Fatalf("resolved Provider = %#v, enabled=%v, error=%v", item, enabled, err)
	}
	candidate.Config = json.RawMessage(`{"issuer":"https://issuer.example","clientId":"kubeloop","claims":{}}`)
	if _, _, err := runtime.provider(candidate); err == nil {
		t.Fatal("missing database client Secret was accepted")
	}

	disabled := adminrevision.ProviderCandidate{ID: "managed", Type: "oidc",
		Config: json.RawMessage(`{"issuer":"https://issuer.example","clientId":"unused","clientSecret":"database-secret","claims":{},"enabled":false}`)}
	install, err := runtime.Prepare(context.Background(), disabled)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Descriptors()) != 0 {
		t.Fatal("Prepare mutated the live Registry")
	}
	install()
	if len(registry.Descriptors()) != 0 || runtime.Check(context.Background()) != nil {
		t.Fatalf("installed descriptors=%v readiness=%v", registry.Descriptors(), runtime.Check(context.Background()))
	}
}
