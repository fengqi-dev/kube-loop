package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSQLiteRestartRecoveryConformance(t *testing.T) {
	config := Config{Backend: BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "restart.db")}
	testRestartRecovery(t, config)
}

func TestPostgreSQLRestartRecoveryConformance(t *testing.T) {
	config, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	testRestartRecovery(t, config)
}

func testRestartRecovery(t *testing.T, config Config) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	persistedID := uuid.NewString()
	if _, err := store.Principals().Upsert(ctx, Principal{
		ID: persistedID, Provider: "oidc-recovery", ExternalID: uuid.NewString(),
		DisplayName: "Persisted", CreatedAt: now,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	rolledBackID := uuid.NewString()
	sentinel := errors.New("force rollback before restart")
	if err := store.WithinTransaction(ctx, func(repositories Repositories) error {
		_, err := repositories.Principals().Upsert(ctx, Principal{
			ID: rolledBackID, Provider: "oidc-recovery", ExternalID: uuid.NewString(), CreatedAt: now,
		})
		if err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		_ = store.Close()
		t.Fatalf("pre-restart rollback error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.Principals().GetByID(ctx, persistedID)
	if err != nil || persisted.DisplayName != "Persisted" {
		t.Fatalf("persisted principal after restart = %#v, %v", persisted, err)
	}
	if _, err := store.Principals().GetByID(ctx, rolledBackID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back principal recovered after restart: %v", err)
	}
}
