package login

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type fakeOIDCProvider struct {
	mu                sync.Mutex
	state             string
	nonce             string
	upstreamChallenge string
	exchangeVerifier  string
	exchangeCalls     int
}

func (provider *fakeOIDCProvider) Descriptor() authn.Descriptor {
	return authn.Descriptor{ID: "corporate", Type: authn.ProviderOIDC, Interaction: authn.InteractionBrowser}
}
func (provider *fakeOIDCProvider) Check(context.Context) error { return nil }
func (provider *fakeOIDCProvider) AuthorizationURL(state, nonce, challenge string) (string, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.state = state
	provider.nonce = nonce
	provider.upstreamChallenge = challenge
	return "https://identity.example.test/authorize?state=" + url.QueryEscape(state), nil
}
func (provider *fakeOIDCProvider) Exchange(_ context.Context, code, verifier, nonce string) (authn.Identity, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.exchangeCalls++
	provider.exchangeVerifier = verifier
	if code != "upstream-code" || nonce != provider.nonce {
		return authn.Identity{}, errors.New("invalid upstream exchange")
	}
	return authn.Identity{
		ProviderID: "corporate", Issuer: "https://identity.example.test", Subject: "stable-subject",
		DisplayName: "Ada", Email: "ada@example.test", Groups: []string{"developers"},
	}, nil
}

func TestLoginServiceCompletesTwoLayerPKCEFlowOnce(t *testing.T) {
	service, provider, store := newTestService(t)
	clientVerifier := strings.Repeat("v", 43)
	clientChallenge := pkceChallenge(clientVerifier)
	request := validBeginRequest(clientChallenge)
	begin, err := service.Begin(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(begin.AuthorizationURL, "https://identity.example.test/authorize") {
		t.Fatalf("authorization URL = %q", begin.AuthorizationURL)
	}
	provider.mu.Lock()
	upstreamState := provider.state
	upstreamChallenge := provider.upstreamChallenge
	provider.mu.Unlock()
	if upstreamChallenge == clientChallenge {
		t.Fatal("desktop PKCE challenge was reused for the upstream OIDC exchange")
	}

	callback, err := service.CompleteCallback(context.Background(), CallbackRequest{
		ProviderID: "corporate", UpstreamCode: "upstream-code", UpstreamState: upstreamState,
	})
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(callback.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	if redirect.Scheme != "http" || redirect.Host != "127.0.0.1:49152" ||
		redirect.Query().Get("state") != request.ClientState {
		t.Fatalf("desktop redirect = %q", callback.RedirectURL)
	}
	exchangeCode := redirect.Query().Get("code")
	result, err := service.Exchange(context.Background(), exchangeCode, clientVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if result.Principal.Provider != "corporate" || result.Principal.DisplayName != "Ada" ||
		result.Principal.ExternalID != "https://identity.example.test\x00stable-subject" {
		t.Fatalf("principal = %#v", result.Principal)
	}
	if _, err := store.Principals().GetByID(context.Background(), result.Principal.ID); err != nil {
		t.Fatalf("principal was not persisted: %v", err)
	}
	if _, err := service.Exchange(context.Background(), exchangeCode, clientVerifier); !errors.Is(err, ErrExpiredOrReplayed) {
		t.Fatalf("exchange replay = %v", err)
	}
	if _, err := service.CompleteCallback(context.Background(), CallbackRequest{
		ProviderID: "corporate", UpstreamCode: "upstream-code", UpstreamState: upstreamState,
	}); !errors.Is(err, ErrExpiredOrReplayed) {
		t.Fatalf("callback replay = %v", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.exchangeCalls != 1 || provider.exchangeVerifier == clientVerifier {
		t.Fatalf("upstream exchange calls=%d verifier=%q", provider.exchangeCalls, provider.exchangeVerifier)
	}
}

func TestLoginServiceRejectsTamperingAndUnsafeCallbacks(t *testing.T) {
	service, provider, _ := newTestService(t)
	challenge := pkceChallenge(strings.Repeat("v", 43))
	for _, callback := range []string{
		"https://127.0.0.1:49152/callback",
		"http://localhost:49152/callback",
		"http://192.168.1.10:49152/callback",
		"http://127.0.0.1/callback",
		"http://127.0.0.1:49152/callback?code=attacker",
	} {
		request := validBeginRequest(challenge)
		request.ClientCallback = callback
		if _, err := service.Begin(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("callback %q error = %v", callback, err)
		}
	}
	request := validBeginRequest(challenge)
	if _, err := service.Begin(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	state := provider.state
	provider.mu.Unlock()
	if _, err := service.CompleteCallback(context.Background(), CallbackRequest{
		ProviderID: "corporate", UpstreamCode: "upstream-code", UpstreamState: state + "tampered",
	}); !errors.Is(err, ErrExpiredOrReplayed) {
		t.Fatalf("tampered state error = %v", err)
	}
	if _, err := service.CompleteCallback(context.Background(), CallbackRequest{
		ProviderID: "other", UpstreamCode: "upstream-code", UpstreamState: state,
	}); !errors.Is(err, ErrExpiredOrReplayed) {
		t.Fatalf("cross-provider callback error = %v", err)
	}
}

func TestLoginServiceAllowsOnlyConfiguredHTTPSBrowserCallback(t *testing.T) {
	service, _, _ := newTestService(t)
	request := validBeginRequest(pkceChallenge(strings.Repeat("v", 43)))
	request.ClientCallback = "https://gateway.example.test/kubeloop/api/admin/ui/callback"
	if _, err := service.Begin(context.Background(), request); err != nil {
		t.Fatalf("configured browser callback error = %v", err)
	}
}

func TestConfiguredBrowserCallbackRejectsRemoteHTTP(t *testing.T) {
	for _, value := range []string{
		"http://gateway.example.test/kubeloop/api/admin/ui/callback",
		"https://gateway.example.test/kubeloop/api/admin/ui/callback?next=attacker",
		"https://user@gateway.example.test/kubeloop/api/admin/ui/callback",
	} {
		if _, err := validateConfiguredCallback(value); err == nil {
			t.Fatalf("configured callback %q was accepted", value)
		}
	}
	for _, value := range []string{
		"https://gateway.example.test/kubeloop/api/admin/ui/callback",
		"http://127.0.0.1:8080/kubeloop/api/admin/ui/callback",
		"http://localhost:8080/kubeloop/api/admin/ui/callback",
	} {
		if _, err := validateConfiguredCallback(value); err != nil {
			t.Fatalf("configured callback %q error=%v", value, err)
		}
	}
}

func TestLoginServiceWrongVerifierConsumesExchangeCode(t *testing.T) {
	service, provider, _ := newTestService(t)
	clientVerifier := strings.Repeat("v", 43)
	if _, err := service.Begin(context.Background(), validBeginRequest(pkceChallenge(clientVerifier))); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	state := provider.state
	provider.mu.Unlock()
	callback, err := service.CompleteCallback(context.Background(), CallbackRequest{
		ProviderID: "corporate", UpstreamCode: "upstream-code", UpstreamState: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	redirect, _ := url.Parse(callback.RedirectURL)
	code := redirect.Query().Get("code")
	if _, err := service.Exchange(context.Background(), code, strings.Repeat("x", 43)); !errors.Is(err, ErrPKCEVerification) {
		t.Fatalf("wrong verifier error = %v", err)
	}
	if _, err := service.Exchange(context.Background(), code, clientVerifier); !errors.Is(err, ErrExpiredOrReplayed) {
		t.Fatalf("burned code replay = %v", err)
	}
}

func newTestService(t *testing.T) (*Service, *fakeOIDCProvider, *storage.Store) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "login.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	provider := &fakeOIDCProvider{}
	registry, err := authn.NewRegistry(provider)
	if err != nil {
		t.Fatal(err)
	}
	values := [][]byte{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)}
	var randomMu sync.Mutex
	service, err := New(registry, store, Config{
		AllowedCallbacks: []string{"https://gateway.example.test/kubeloop/api/admin/ui/callback"},
		Now:              func() time.Time { return time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC) },
		Random: func(target []byte) error {
			randomMu.Lock()
			defer randomMu.Unlock()
			if len(values) == 0 {
				return errors.New("random queue exhausted")
			}
			copy(target, values[0])
			values = values[1:]
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, provider, store
}

func validBeginRequest(challenge string) BeginRequest {
	return BeginRequest{
		ProviderID: "corporate", ClientCallback: "http://127.0.0.1:49152/callback",
		ClientState: strings.Repeat("s", 43), Nonce: strings.Repeat("n", 43), PKCEChallenge: challenge,
	}
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
