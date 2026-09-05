package trojanws

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

func TestDerivePasswordIsStableAndDomainSeparated(t *testing.T) {
	t.Parallel()

	const ticket = "header.payload.signature"
	digest := sha256.Sum256([]byte(passwordDomain + ticket))
	want := hex.EncodeToString(digest[:])

	got, err := DerivePassword(ticket)
	if err != nil {
		t.Fatalf("DerivePassword() error = %v", err)
	}
	if got != want {
		t.Fatalf("DerivePassword() = %q, want %q", got, want)
	}
	if len(got) != sha256.Size*2 {
		t.Fatalf("password length = %d, want %d", len(got), sha256.Size*2)
	}
	legacy := sha256.Sum256([]byte(ticket))
	if got == hex.EncodeToString(legacy[:]) {
		t.Fatal("password is not domain-separated")
	}
}

func TestDerivePasswordChangesWithRelayTicket(t *testing.T) {
	t.Parallel()

	first, err := DerivePassword("header.payload.signature-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := DerivePassword("header.payload.signature-b")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different RelayTickets derived the same password")
	}
}

func TestDerivePasswordRejectsInvalidRelayTicket(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":      "",
		"whitespace": " ticket",
		"line break": "ticket\nvalue",
		"oversized":  strings.Repeat("a", relayticket.MaximumTicketBytes+1),
	}
	for name, ticket := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DerivePassword(ticket); err == nil {
				t.Fatal("DerivePassword() error = nil")
			}
		})
	}
}

func TestDeriveSessionPasswordIsStablePerGeneration(t *testing.T) {
	t.Parallel()

	const sessionID = "33333333-3333-4333-8333-333333333333"
	first, err := DeriveSessionPassword(sessionID, 7)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DeriveSessionPassword(sessionID, 7)
	if err != nil {
		t.Fatal(err)
	}
	next, err := DeriveSessionPassword(sessionID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if first != again || first == next || len(first) != sha256.Size*2 {
		t.Fatalf("unexpected Session passwords: first=%q again=%q next=%q", first, again, next)
	}
}

func TestDeriveSessionPasswordRejectsInvalidSession(t *testing.T) {
	t.Parallel()

	if _, err := DeriveSessionPassword("not-a-uuid", 1); err == nil {
		t.Fatal("invalid Session ID accepted")
	}
	if _, err := DeriveSessionPassword("33333333-3333-4333-8333-333333333333", 0); err == nil {
		t.Fatal("zero generation accepted")
	}
}
