package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
)

func TestManagementBootstrapRetirementIsPersistentAndIrreversible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management-state.db")
	store := openSQLiteTestStore(t, path)
	ctx := context.Background()
	if retired, err := store.ManagementState().BootstrapRetired(ctx); err != nil || retired {
		t.Fatalf("initial retired = %t, error = %v", retired, err)
	}
	retiredAt := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	changed, err := store.ManagementState().RetireBootstrap(ctx, retiredAt)
	if err != nil || !changed {
		t.Fatalf("first retirement changed = %t, error = %v", changed, err)
	}
	changed, err = store.ManagementState().RetireBootstrap(ctx, retiredAt.Add(time.Hour))
	if err != nil || changed {
		t.Fatalf("second retirement changed = %t, error = %v", changed, err)
	}
	var storedAt string
	if err := store.db.QueryRowContext(ctx,
		`SELECT bootstrap_retired_at FROM management_metadata WHERE id = 1`,
	).Scan(&storedAt); err != nil {
		t.Fatal(err)
	}
	if storedAt != formatTime(retiredAt) {
		t.Fatalf("retirement marker = %q", storedAt)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openSQLiteTestStore(t, path)
	if retired, err := reopened.ManagementState().BootstrapRetired(ctx); err != nil || !retired {
		t.Fatalf("reopened retired = %t, error = %v", retired, err)
	}
}

func TestManagementBootstrapRetirementRollsBackWithTransaction(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "management-transaction.db"))
	ctx := context.Background()
	wantError := errors.New("publish failed")
	err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		changed, retireErr := repositories.ManagementState().RetireBootstrap(ctx, time.Now().UTC())
		if retireErr != nil || !changed {
			t.Fatalf("transaction retirement changed = %t, error = %v", changed, retireErr)
		}
		return wantError
	})
	if !errors.Is(err, wantError) {
		t.Fatalf("transaction error = %v", err)
	}
	if retired, err := store.ManagementState().BootstrapRetired(ctx); err != nil || retired {
		t.Fatalf("rolled back retired = %t, error = %v", retired, err)
	}
}

func TestManagementStateDrivesBootstrapAuthorizationFailClosed(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "management-authorizer.db"))
	engine, err := adminauthorization.NewDenyAll(adminauthorization.WithBootstrap(
		adminauthorization.BootstrapConfig{Subjects: []string{"00000000-0000-4000-8000-000000000001"}}, store.ManagementState(),
	))
	if err != nil {
		t.Fatal(err)
	}
	request := adminauthorization.Request{
		Resource: adminauthorization.ResourceAssignment, Operation: adminauthorization.OperationCreate,
	}
	subject := adminauthorization.Subject{ID: "00000000-0000-4000-8000-000000000001"}
	if decision := engine.Authorize(context.Background(), subject, request); !decision.Allowed {
		t.Fatalf("initial bootstrap decision = %#v", decision)
	}
	if _, err := store.ManagementState().RetireBootstrap(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if decision := engine.Authorize(context.Background(), subject, request); decision.Allowed || decision.Reason != adminauthorization.ReasonBootstrapRetired {
		t.Fatalf("retired bootstrap decision = %#v", decision)
	}
	if _, err := store.db.ExecContext(context.Background(), `DROP TABLE management_metadata`); err != nil {
		t.Fatal(err)
	}
	if decision := engine.Authorize(context.Background(), subject, request); decision.Allowed || decision.Reason != adminauthorization.ReasonBootstrapStateUnavailable {
		t.Fatalf("unavailable bootstrap decision = %#v", decision)
	}
}

func TestManagementBootstrapRetirementRejectsInvalidInput(t *testing.T) {
	store := openSQLiteTestStore(t, filepath.Join(t.TempDir(), "management-invalid.db"))
	if _, err := store.ManagementState().RetireBootstrap(context.Background(), time.Time{}); err == nil {
		t.Fatal("zero retirement time succeeded")
	}
}
