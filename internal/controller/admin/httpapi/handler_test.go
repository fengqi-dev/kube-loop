package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controller/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
)

func TestBreakGlassExchangeSetsHostCookieAndReturnsCSRF(t *testing.T) {
	handler, _ := newTestHandler(t, Config{PublicURL: "https://gateway.example/base"}, true)
	request := exchangeRequest(`{"credential":"valid"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := recorder.Result()
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || cookie.Value == "" || cookie.Path != "/" ||
		!cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge < 1 {
		t.Fatalf("cookie=%+v", cookie)
	}
	var document struct {
		CSRFToken string `json:"csrfToken"`
		ExpiresAt string `json:"expiresAt"`
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.CSRFToken == "" || document.ExpiresAt == "" || document.RequestID == "" ||
		response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("response=%+v headers=%v", document, response.Header)
	}
}

func TestDisabledAndWrongBreakGlassCredentialsAreIndistinguishable(t *testing.T) {
	wrongHandler, _ := newTestHandler(t, Config{PublicURL: "https://gateway.example"}, true)
	disabledHandler, _ := newTestHandler(t, Config{PublicURL: "https://gateway.example"}, false)
	wrong := performExchange(wrongHandler, `{"credential":"wrong"}`)
	disabled := performExchange(disabledHandler, `{"credential":"valid"}`)
	if wrong.Code != http.StatusUnauthorized || disabled.Code != http.StatusUnauthorized {
		t.Fatalf("statuses wrong=%d disabled=%d", wrong.Code, disabled.Code)
	}
	if errorSignature(t, wrong) != errorSignature(t, disabled) {
		t.Fatalf("wrong=%s disabled=%s", wrong.Body.String(), disabled.Body.String())
	}
}

func TestBreakGlassExchangeEnforcesOriginShapeAndRateLimit(t *testing.T) {
	handler, _ := newTestHandler(t, Config{
		PublicURL: "https://gateway.example", GlobalAttempts: 2, SourceAttempts: 1, RateLimitWindow: time.Minute,
	}, true)
	crossSite := exchangeRequest(`{"credential":"valid"}`)
	crossSite.Header.Set("Origin", "https://attacker.example")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, crossSite)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("cross-site status=%d", first.Code)
	}
	second := performExchange(handler, `{"credential":"valid"}`)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limit status=%d body=%s", second.Code, second.Body.String())
	}

	strictHandler, _ := newTestHandler(t, Config{PublicURL: "https://gateway.example"}, true)
	unknown := performExchange(strictHandler, `{"credential":"valid","extra":true}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestLimiterSeparatesSourcesAndBoundsBuckets(t *testing.T) {
	limiter := newExchangeLimiter(maximumSourceBuckets+2, 1, time.Minute)
	if !limiter.allow("192.0.2.1") || limiter.allow("192.0.2.1") || !limiter.allow("192.0.2.2") {
		t.Fatal("source limiter decisions are invalid")
	}
	for index := 0; index < maximumSourceBuckets+10; index++ {
		_ = limiter.allow(string(rune(index + 1)))
	}
	if len(limiter.sources) > maximumSourceBuckets {
		t.Fatalf("source buckets=%d", len(limiter.sources))
	}
}

func TestManagementPublicURLRequiresTLSOutsideLoopback(t *testing.T) {
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	generation := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	sessions, _ := adminsession.New(store, &testVerifier{enabled: true, generation: generation})
	if _, err := New(Config{PublicURL: "http://gateway.example"}, sessions); err == nil {
		t.Fatal("non-loopback HTTP public URL was accepted")
	}
	if _, err := New(Config{PublicURL: "http://127.0.0.1:8080"}, sessions); err != nil {
		t.Fatalf("loopback development URL: %v", err)
	}
}

type testVerifier struct {
	enabled    bool
	generation string
}

func (verifier *testVerifier) Verify(_ context.Context, _ netip.Addr, supplied []byte) (string, error) {
	defer clear(supplied)
	if !verifier.enabled || !bytes.Equal(supplied, []byte("valid")) {
		return "", adminsession.ErrAuthenticationFailed
	}
	return verifier.generation, nil
}

func (*testVerifier) SessionTTL() time.Duration { return 5 * time.Minute }

func (verifier *testVerifier) CurrentBreakGlassState(context.Context) (adminauthorization.BreakGlassState, error) {
	return adminauthorization.BreakGlassState{Enabled: verifier.enabled, Generation: verifier.generation}, nil
}

func newTestHandler(t *testing.T, config Config, enabled bool) (*Handler, *storage.Store) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	generation := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	sessions, err := adminsession.New(store, &testVerifier{enabled: enabled, generation: generation})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(config, sessions)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func exchangeRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/sessions/break-glass", bytes.NewBufferString(body))
	request.RemoteAddr = "192.0.2.20:43210"
	request.Header.Set("Origin", "https://gateway.example")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func performExchange(handler http.Handler, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, exchangeRequest(body))
	return recorder
}

func errorSignature(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var document struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document.Error.Code + "\x00" + document.Error.Message
}
