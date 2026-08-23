package bootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestCompleteDefaultCreatesFirstUser(t *testing.T) {
	service, localUsers := newBootstrapTestService(t)
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

func TestEnsureTokenCompletesBootstrapExactlyOnce(t *testing.T) {
	service, localUsers := newBootstrapTestService(t)
	ctx := context.Background()
	token, expiresAt, err := service.EnsureToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || !expiresAt.After(time.Now()) {
		t.Fatalf("issued token length=%d expires=%s", len(token), expiresAt)
	}
	secondToken, secondExpiry, err := service.EnsureToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if secondToken != "" || !secondExpiry.Equal(expiresAt) {
		t.Fatalf("reissued token length=%d expires=%s", len(secondToken), secondExpiry)
	}
	password := []byte("correct-horse-battery-staple")
	defer clear(password)
	result, err := service.Complete(ctx, CompleteRequest{
		Token: token, Username: "admin", Password: password,
		DisplayName: "Administrator", RequestID: uuid.NewString(),
	})
	if err != nil || result.Identity.IdentityID == "" {
		t.Fatalf("complete result=%+v err=%v", result, err)
	}
	if _, err := localUsers.Authenticate(ctx, "admin", []byte("correct-horse-battery-staple")); err != nil {
		t.Fatalf("authenticate bootstrapped administrator: %v", err)
	}
	if _, err := service.Complete(ctx, CompleteRequest{
		Token: token, Username: "second", Password: []byte("another-correct-password"),
		DisplayName: "Second", RequestID: uuid.NewString(),
	}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("bootstrap token reuse error = %v", err)
	}
}

func newBootstrapTestService(t *testing.T) (*Service, *adminlocaluser.Service) {
	t.Helper()
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
	return service, localUsers
}
