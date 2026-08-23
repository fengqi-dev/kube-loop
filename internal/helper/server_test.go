package helper

import (
	"errors"
	"testing"

	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func TestDispatchRejectsLegacyExecutableRequest(t *testing.T) {
	server := NewServer(AuthFile{Token: "secret"})
	response := server.dispatch(helperprotocol.Request{Op: helperprotocol.OpStart, Token: "secret"})
	if response.OK || response.Error != "session is required" {
		t.Fatalf("dispatch() = %#v", response)
	}
}

func TestDispatchRequiresValidSessionIDForStop(t *testing.T) {
	server := NewServer(AuthFile{Token: "secret"})
	response := server.dispatch(helperprotocol.Request{
		Op: helperprotocol.OpStop, Token: "secret", SessionID: "../../session",
	})
	if response.OK {
		t.Fatalf("dispatch() unexpectedly accepted an unsafe session ID")
	}
}

func TestStartSessionRejectsServerClosing(t *testing.T) {
	server := NewServer(AuthFile{})
	server.closing.Store(true)
	if err := server.startSession(singbox.SessionSpec{}); !errors.Is(err, errServerClosing) {
		t.Fatalf("startSession error = %v, want %v", err, errServerClosing)
	}
}

func TestTailText(t *testing.T) {
	if got := tailText([]byte("short"), 8); got != "short" {
		t.Fatalf("short tail = %q", got)
	}
	if got := tailText([]byte("0123456789"), 4); got != "6789" {
		t.Fatalf("truncated tail = %q", got)
	}
	if got := tailText([]byte("0123456789"), 0); got != "0123456789" {
		t.Fatalf("unlimited tail = %q", got)
	}
}
