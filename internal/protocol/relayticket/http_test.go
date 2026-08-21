package relayticket

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestVerifierConsumesRelayTicketOnce(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := signer.Sign(validClaims(now))
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Keys: map[string]ed25519.PublicKey{"primary": publicKey}, Issuer: "https://control-plane.example",
		Audience: "relay-a", RequiredOperation: "tunnel", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NewReplayGuard(16, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	requestVerifier, err := NewRequestVerifier(verifier, replay)
	if err != nil {
		t.Fatal(err)
	}

	const attempts = 16
	var accepted atomic.Int32
	var wait sync.WaitGroup
	for range attempts {
		wait.Go(func() {
			request := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
			request.Header.Set("Authorization", "Bearer "+ticket)
			if _, verifyErr := requestVerifier.Verify(request); verifyErr == nil {
				accepted.Add(1)
			}
		})
	}
	wait.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted = %d, want 1", accepted.Load())
	}
}

func TestRequestVerifierRejectsOlderSessionGeneration(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Keys: map[string]ed25519.PublicKey{"primary": publicKey}, Issuer: "https://control-plane.example",
		Audience: "relay-a", RequiredOperation: "tunnel", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NewReplayGuard(16, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	requestVerifier, err := NewRequestVerifier(verifier, replay)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(generation uint64, id string) string {
		claims := validClaims(now)
		claims.SessionGeneration = generation
		claims.TicketID = id
		ticket, signErr := signer.Sign(claims)
		if signErr != nil {
			t.Fatal(signErr)
		}
		return ticket
	}
	verify := func(ticket string) error {
		request := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
		request.Header.Set("Authorization", "Bearer "+ticket)
		_, verifyErr := requestVerifier.Verify(request)
		return verifyErr
	}
	if err := verify(issue(8, "ticket-generation-8")); err != nil {
		t.Fatalf("new generation rejected: %v", err)
	}
	if err := verify(issue(7, "ticket-generation-7")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("older generation accepted: %v", err)
	}
	if err := verify(issue(8, "ticket-generation-8-second")); err != nil {
		t.Fatalf("current generation rejected: %v", err)
	}
}

func TestSessionGenerationGuardPurgesOnlyExpiredEntries(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	current := now
	guard, err := NewSessionGenerationGuard(2, func() time.Time { return current })
	if err != nil {
		t.Fatal(err)
	}
	if !guard.Accept("session-a", 2, now.Add(time.Second)) ||
		!guard.Accept("session-b", 4, now.Add(time.Minute)) {
		t.Fatal("initial Session generations were rejected")
	}
	if guard.Accept("session-c", 1, now.Add(time.Minute)) {
		t.Fatal("live Session generation was evicted at capacity")
	}
	if guard.Accept("session-b", 3, now.Add(time.Minute)) {
		t.Fatal("stale Session generation was accepted")
	}
	current = now.Add(2 * time.Second)
	if !guard.Accept("session-c", 1, now.Add(time.Minute)) {
		t.Fatal("expired Session generation was not purged")
	}
	if !guard.Accept("session-b", 5, now.Add(time.Minute)) {
		t.Fatal("newer Session generation was rejected")
	}
	if guard.Accept(strings.Repeat("x", 129), 1, now.Add(time.Minute)) {
		t.Fatal("invalid Session ID was accepted")
	}
}

func TestReplayGuardPurgesExpiredEntriesWithoutEvictingLiveTickets(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	current := now
	guard, err := NewReplayGuard(2, func() time.Time { return current })
	if err != nil {
		t.Fatal(err)
	}
	if !guard.Consume("ticket-a", now.Add(time.Second)) || !guard.Consume("ticket-b", now.Add(time.Minute)) {
		t.Fatal("initial tickets were rejected")
	}
	if guard.Consume("ticket-c", now.Add(time.Minute)) {
		t.Fatal("live ticket was evicted when replay cache was full")
	}
	current = now.Add(2 * time.Second)
	if !guard.Consume("ticket-c", now.Add(time.Minute)) {
		t.Fatal("expired replay entry was not purged")
	}
	if guard.Consume("ticket-b", now.Add(time.Minute)) {
		t.Fatal("replayed live ticket was accepted")
	}
}
