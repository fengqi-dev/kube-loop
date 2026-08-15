package iam

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
)

func TestLoaderLoadsGroupNamespaceAccess(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "iam.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	organizationID, groupID := uuid.NewString(), uuid.NewString()
	if err := store.Organizations().Create(ctx, storage.Organization{
		ID: organizationID, Name: "KubeLoop", Slug: "kubeloop", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Groups().Create(ctx, storage.Group{
		ID: groupID, OrganizationID: organizationID, Name: "Developers", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Groups().PutNamespace(ctx, storage.GroupNamespace{
		GroupID: groupID, Namespace: "development", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := adminauthorization.NewDenyAll()
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewLoader(store, engine)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Load(ctx); err != nil {
		t.Fatal(err)
	}
	decision := engine.Authorize(ctx, adminauthorization.Subject{
		ID: uuid.NewString(), Groups: []string{groupID}, Authentication: adminauthorization.AuthenticationNormal,
	}, adminauthorization.Request{Capability: "namespace.resources.read", Namespace: "development"})
	if !decision.Allowed {
		t.Fatalf("authorization decision = %+v", decision)
	}
}
