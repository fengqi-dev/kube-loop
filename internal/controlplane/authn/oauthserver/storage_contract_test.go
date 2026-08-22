package oauthserver

import (
	"errors"
	"testing"
	"time"

	"github.com/ory/fosite"
)

func TestStorageExplicitAndFallbackTransactionContracts(t *testing.T) {
	_, store, _ := newTestEndpoints(t)
	storage, err := NewStorage(store)
	if err != nil {
		t.Fatal(err)
	}
	transactionContext, err := storage.BeginTX(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if storage.repositoriesFor(transactionContext) == storage.repositories {
		t.Fatal("explicit transaction did not bind transactional repositories")
	}
	if err := storage.Rollback(transactionContext); err != nil {
		t.Fatal(err)
	}

	if err := storage.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := storage.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestStorageRejectsClientAssertionJWTContracts(t *testing.T) {
	_, store, _ := newTestEndpoints(t)
	storage, err := NewStorage(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.ClientAssertionJWTValid(t.Context(), "assertion-id"); !errors.Is(err, fosite.ErrNotFound) {
		t.Fatalf("client assertion validation error = %v", err)
	}
	if err := storage.SetClientAssertionJWT(
		t.Context(),
		"assertion-id",
		time.Now().Add(time.Minute),
	); !errors.Is(err, fosite.ErrNotFound) {
		t.Fatalf("client assertion storage error = %v", err)
	}
}
