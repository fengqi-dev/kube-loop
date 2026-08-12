package main

import (
	"context"
	"path/filepath"
	"testing"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestEnsureInitialAdminPolicyIsOneTime(t *testing.T) {
	ctx := context.Background()
	store, err := controlplanestorage.Open(ctx, controlplanestorage.Config{
		Backend: controlplanestorage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "state.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	users, err := adminlocaluser.New(store, []byte("0123456789abcdef0123456789abcdef"), "KubeLoop Test")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := users.Create(ctx, adminlocaluser.CreateRequest{Username: "admin", Password: []byte("initial-password-value")})
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := adminrevision.New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureInitialAdminPolicy(ctx, revisions, admin.PrincipalID); err != nil {
		t.Fatal(err)
	}
	first, err := revisions.CurrentPolicy(ctx)
	if err != nil || !first.Active || len(first.Snapshot.Assignments) != 1 ||
		first.Snapshot.Assignments[0].Role != adminauthorization.RolePlatformAdmin {
		t.Fatalf("initial policy = %#v, %v", first, err)
	}
	if err := ensureInitialAdminPolicy(ctx, revisions, admin.PrincipalID); err != nil {
		t.Fatal(err)
	}
	second, err := revisions.CurrentPolicy(ctx)
	if err != nil || second.Pointer.ETag != first.Pointer.ETag || second.Pointer.Revision != first.Pointer.Revision {
		t.Fatalf("repeated bootstrap changed policy: first=%#v second=%#v err=%v", first.Pointer, second.Pointer, err)
	}
}
