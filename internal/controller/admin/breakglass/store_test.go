package breakglass

import (
	"context"
	"encoding/base64"
	"errors"
	"net/netip"
	"testing"
	"time"

	adminconfig "github.com/fengqi-dev/kube-loop/internal/controller/admin/config"
)

func TestVerifyAndSecretRotationChangeGeneration(t *testing.T) {
	first := encodedCredential(0x11)
	current := append([]byte(nil), first...)
	store := &Store{
		enabled: true, secretFile: "credential", sessionTTL: 10 * time.Minute,
		readFile: func(string) ([]byte, error) { return append([]byte(nil), current...), nil },
	}
	supplied := append([]byte(nil), first...)
	firstGeneration, err := store.Verify(context.Background(), netip.MustParseAddr("192.0.2.1"), supplied)
	if err != nil || firstGeneration == "" {
		t.Fatalf("first verification generation = %q, error = %v", firstGeneration, err)
	}
	for index, value := range supplied {
		if value != 0 {
			t.Fatalf("supplied credential byte %d was not cleared", index)
		}
	}
	state, err := store.CurrentBreakGlassState(context.Background())
	if err != nil || !state.Enabled || state.Generation != firstGeneration {
		t.Fatalf("first state = %#v, error = %v", state, err)
	}
	current = encodedCredential(0x22)
	rotatedState, err := store.CurrentBreakGlassState(context.Background())
	if err != nil || rotatedState.Generation == firstGeneration {
		t.Fatalf("rotated state = %#v, error = %v", rotatedState, err)
	}
	if _, err := store.Verify(context.Background(), netip.MustParseAddr("192.0.2.1"), append([]byte(nil), first...)); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("old credential error = %v", err)
	}
}

func TestVerifyEnforcesSourceCIDRAndUniformCredentialFailure(t *testing.T) {
	credential := encodedCredential(0x33)
	store := &Store{
		enabled: true, secretFile: "credential", sessionTTL: 15 * time.Minute,
		sourceCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		readFile:    func(string) ([]byte, error) { return append([]byte(nil), credential...), nil },
	}
	if _, err := store.Verify(context.Background(), netip.MustParseAddr("192.0.2.1"), append([]byte(nil), credential...)); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("source rejection error = %v", err)
	}
	if _, err := store.Verify(context.Background(), netip.MustParseAddr("10.1.2.3"), []byte("not-a-credential")); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("malformed credential error = %v", err)
	}
	wrong := encodedCredential(0x44)
	if _, err := store.Verify(context.Background(), netip.MustParseAddr("10.1.2.3"), wrong); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("wrong credential error = %v", err)
	}
}

func TestCredentialFileFailuresDoNotExposeCause(t *testing.T) {
	tests := []struct {
		name string
		read func(string) ([]byte, error)
	}{
		{name: "missing", read: func(string) ([]byte, error) { return nil, errors.New("secret path and cause") }},
		{name: "short", read: func(string) ([]byte, error) { return []byte("short"), nil }},
		{name: "oversized", read: func(string) ([]byte, error) { return make([]byte, maximumCredentialFileBytes+1), nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &Store{enabled: true, secretFile: "sensitive-path", readFile: test.read}
			_, err := store.CurrentBreakGlassState(context.Background())
			if !errors.Is(err, ErrUnavailable) || err.Error() != ErrUnavailable.Error() {
				t.Fatalf("state error = %v", err)
			}
		})
	}
}

func TestNewDisabledAndEnabledConfiguration(t *testing.T) {
	disabled, err := New(adminconfig.BreakGlassConfig{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := disabled.CurrentBreakGlassState(context.Background())
	if err != nil || state.Enabled {
		t.Fatalf("disabled state = %#v, error = %v", state, err)
	}
	if _, err := New(adminconfig.BreakGlassConfig{Enabled: true}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("incomplete enabled config error = %v", err)
	}
}

func encodedCredential(fill byte) []byte {
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = fill + byte(index)
	}
	return []byte(base64.RawURLEncoding.EncodeToString(raw))
}
