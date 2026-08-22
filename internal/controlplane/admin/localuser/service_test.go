package localuser

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestPasswordCredentialsAuthenticate(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "local-users.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	password := []byte("correct-horse-battery")
	created, err := service.Create(ctx, CreateRequest{
		Username: "ui-test", Password: password, DisplayName: "UI Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "ui-test", password); err != nil {
		t.Fatalf("credential authentication failed: %v", err)
	}
	users, err := service.List(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("local users = %#v, %v", users, err)
	}
	if err := service.SetEnabled(ctx, users[0].IdentityID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "ui-test", password); !errors.Is(
		err,
		ErrDisabled,
	) {
		t.Fatalf("disabled authentication error = %v", err)
	}
	users, err = service.List(ctx)
	if err != nil || len(users) != 1 || users[0].Enabled {
		t.Fatalf("disabled local users = %#v, %v", users, err)
	}
	if err := service.SetEnabled(ctx, users[0].IdentityID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "ui-test", password); err != nil {
		t.Fatalf("re-enabled authentication failed: %v", err)
	}
	loaded, err := service.Get(ctx, created.IdentityID)
	if err != nil || loaded.IdentityID != created.IdentityID || loaded.Username != created.Username {
		t.Fatalf("loaded user = %#v, %v", loaded, err)
	}
	if err := service.SetPassword(ctx, created.IdentityID, []byte("too-short")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password error = %v", err)
	}
	newPassword := []byte("new-correct-horse-battery")
	if err := service.SetPassword(ctx, created.IdentityID, newPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "ui-test", password); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("old password authentication error = %v", err)
	}
	if _, err := service.Authenticate(ctx, "ui-test", newPassword); err != nil {
		t.Fatalf("new password authentication failed: %v", err)
	}
}
