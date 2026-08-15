package localuser

import (
	"context"
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
	_, err = service.Create(ctx, CreateRequest{
		Username: "ui-test", Password: password, DisplayName: "UI Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "ui-test", password); err != nil {
		t.Fatalf("credential authentication failed: %v", err)
	}
}
