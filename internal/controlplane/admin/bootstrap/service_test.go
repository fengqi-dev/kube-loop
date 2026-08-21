package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestCompleteDefaultCreatesFirstUser(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "bootstrap.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	localUsers, err := adminlocaluser.New(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, localUsers)
	if err != nil {
		t.Fatal(err)
	}
	result, password, created, err := service.CompleteDefault(
		context.Background(),
		DefaultRequest{
			Username: "admin", Password: []byte("correct-horse-battery-staple"),
			DisplayName: "Administrator", RequestID: uuid.NewString(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created || password != "" || result.Identity.IdentityID == "" {
		t.Fatalf(
			"bootstrap result = %+v, password=%q, created=%t",
			result,
			password,
			created,
		)
	}
	if _, err := localUsers.Authenticate(
		context.Background(),
		"admin",
		[]byte("correct-horse-battery-staple"),
	); err != nil {
		t.Fatalf("authenticate default administrator: %v", err)
	}
}
