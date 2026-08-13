package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestReadBinarySecretFilePreservesWhitespaceBytes(t *testing.T) {
	want := bytes.Repeat([]byte{0x42}, 32)
	want[0], want[len(want)-1] = '\n', ' '
	path := filepath.Join(t.TempDir(), "mfa-key")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readBinarySecretFile(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binary Secret changed: got %x, want %x", got, want)
	}
}

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
	if err != nil || !first.Active || len(first.Snapshot.Bindings) != 1 ||
		first.Snapshot.Bindings[0].RoleID != adminauthorization.RolePlatformAdmin {
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
