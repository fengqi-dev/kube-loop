package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	admintoken "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func TestBreakGlassSessionRouteIsNotRegistered(t *testing.T) {
	handler, _ := newTestHandler(t, Config{PublicURL: "https://gateway.example"}, true)
	response := performExchange(handler, `{"credential":"valid"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLimiterSeparatesSourcesAndBoundsBuckets(t *testing.T) {
	limiter := newExchangeLimiter(maximumSourceBuckets+2, 1, time.Minute)
	if !limiter.allow("192.0.2.1") || limiter.allow("192.0.2.1") || !limiter.allow("192.0.2.2") {
		t.Fatal("source limiter decisions are invalid")
	}
	for index := range maximumSourceBuckets + 10 {
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
	sessions, _ := adminsession.New(store)
	if _, err := New(Config{PublicURL: "http://gateway.example"}, sessions); err == nil {
		t.Fatal("non-loopback HTTP public URL was accepted")
	}
	if _, err := New(Config{PublicURL: "http://127.0.0.1:8080"}, sessions); err != nil {
		t.Fatalf("loopback development URL: %v", err)
	}
}

func TestReadAPIRequiresCookieAndReturnsAuthorizedCapabilitiesAndStatus(t *testing.T) {
	handler, store := newReadTestHandler(t, true)
	unauthenticated := httptest.NewRecorder()
	serveHTTP(handler, unauthenticated, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}

	cookie, _ := issueLegacySession(t, handler)
	capabilityRequest := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	capabilityRequest.AddCookie(cookie)
	capabilityRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	capabilities := httptest.NewRecorder()
	serveHTTP(handler, capabilities, capabilityRequest)
	if capabilities.Code != http.StatusOK || capabilities.Header().Get(managementRequestHeader) == "" {
		t.Fatalf("capability status=%d headers=%v body=%s", capabilities.Code, capabilities.Header(), capabilities.Body.String())
	}
	var capabilityDocument struct {
		AuthenticationType string   `json:"authenticationType"`
		Capabilities       []string `json:"capabilities"`
	}
	if err := json.Unmarshal(capabilities.Body.Bytes(), &capabilityDocument); err != nil {
		t.Fatal(err)
	}
	if capabilityDocument.AuthenticationType != "break-glass" || len(capabilityDocument.Capabilities) == 0 {
		t.Fatalf("capability document=%+v", capabilityDocument)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/status", nil)
	statusRequest.AddCookie(cookie)
	status := httptest.NewRecorder()
	serveHTTP(handler, status, statusRequest)
	if status.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	var statusDocument struct {
		ControlPlane struct {
			Version string `json:"version"`
		} `json:"controlPlane"`
		Storage struct {
			Backend       string `json:"backend"`
			SchemaVersion int    `json:"schemaVersion"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &statusDocument); err != nil {
		t.Fatal(err)
	}
	if statusDocument.ControlPlane.Version != "v2-test" || statusDocument.Storage.Backend != "sqlite" || statusDocument.Storage.SchemaVersion < 9 {
		t.Fatalf("status document=%+v", statusDocument)
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Limit: 100})
	if err != nil || len(events) != 3 {
		t.Fatalf("read API audit events=%d error=%v", len(events), err)
	}
}

func TestReadAPIRejectsCrossSiteDuplicateCookieAndUnauthorizedRole(t *testing.T) {
	handler, _ := newReadTestHandler(t, false)
	cookie, _ := issueLegacySession(t, handler)

	crossSite := httptest.NewRequest(http.MethodGet, "/status", nil)
	crossSite.AddCookie(cookie)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteRecorder := httptest.NewRecorder()
	serveHTTP(handler, crossSiteRecorder, crossSite)
	if crossSiteRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("cross-site status=%d", crossSiteRecorder.Code)
	}

	duplicate := httptest.NewRequest(http.MethodGet, "/status", nil)
	duplicate.Header.Add("Cookie", SessionCookieName+"="+cookie.Value+"; "+SessionCookieName+"="+cookie.Value)
	duplicateRecorder := httptest.NewRecorder()
	serveHTTP(handler, duplicateRecorder, duplicate)
	if duplicateRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate-cookie status=%d", duplicateRecorder.Code)
	}

	denied := httptest.NewRequest(http.MethodGet, "/status", nil)
	denied.AddCookie(cookie)
	deniedRecorder := httptest.NewRecorder()
	serveHTTP(handler, deniedRecorder, denied)
	if deniedRecorder.Code != http.StatusForbidden {
		t.Fatalf("denied status=%d body=%s", deniedRecorder.Code, deniedRecorder.Body.String())
	}
}

func TestAuthenticatedManagementWritesRequireSynchronousCSRFToken(t *testing.T) {
	handler, _ := newReadTestHandler(t, true)
	cookie, csrf := issueLegacySession(t, handler)
	protected := echo.New()
	protected.POST("/future-write", func(ctx *echo.Context) error {
		return ctx.NoContent(http.StatusNoContent)
	}, handler.readAPI.authenticate)

	missing := httptest.NewRequest(http.MethodPost, "/future-write", nil)
	missing.AddCookie(cookie)
	missing.Header.Set("Origin", "https://gateway.example")
	missingRecorder := httptest.NewRecorder()
	protected.ServeHTTP(missingRecorder, missing)
	if missingRecorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", missingRecorder.Code)
	}

	valid := httptest.NewRequest(http.MethodPost, "/future-write", nil)
	valid.AddCookie(cookie)
	valid.Header.Set("Origin", "https://gateway.example")
	valid.Header.Set(CSRFHeaderName, csrf)
	validRecorder := httptest.NewRecorder()
	protected.ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF status=%d body=%s", validRecorder.Code, validRecorder.Body.String())
	}
}

func TestGatewayTokenExchangeCreatesNormalAndBootstrapManagementSessions(t *testing.T) {
	for _, bootstrap := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "bootstrap"}[bootstrap], func(t *testing.T) {
			handler, store := newPrincipalTokenHandler(t, bootstrap)
			request := httptest.NewRequest(http.MethodPost, "/sessions/token", bytes.NewBufferString(`{}`))
			request.RemoteAddr = "192.0.2.30:5000"
			request.Header.Set("Origin", "https://gateway.example")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer valid-access-token")
			recorder := httptest.NewRecorder()
			serveHTTP(handler, recorder, request)
			if recorder.Code != http.StatusCreated || len(recorder.Result().Cookies()) != 1 {
				t.Fatalf("exchange status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var issued struct {
				CSRFToken string `json:"csrfToken"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &issued); err != nil || issued.CSRFToken == "" {
				t.Fatalf("exchange response=%s error=%v", recorder.Body.String(), err)
			}
			cookie := recorder.Result().Cookies()[0]
			digest := sha256.Sum256([]byte(cookie.Value))
			stored, err := store.AdminSessions().GetByHash(context.Background(), digest[:])
			if err != nil {
				t.Fatal(err)
			}
			wantAuthentication := "normal"
			if bootstrap {
				wantAuthentication = "bootstrap"
			}
			if stored.AuthenticationType != wantAuthentication || stored.PrincipalID == "" || stored.TokenFamilyID == "" {
				t.Fatalf("stored session=%+v", stored)
			}
			statusRequest := httptest.NewRequest(http.MethodGet, "/status", nil)
			statusRequest.AddCookie(cookie)
			status := httptest.NewRecorder()
			serveHTTP(handler, status, statusRequest)
			if status.Code != http.StatusOK {
				t.Fatalf("status after exchange=%d body=%s", status.Code, status.Body.String())
			}
			events, err := store.Audit().List(context.Background(), storage.AuditFilter{
				Action: "admin.session.principal.exchange",
			})
			if err != nil || len(events) != 1 || events[0].Outcome != "success" {
				t.Fatalf("exchange audit=%+v error=%v", events, err)
			}
			logoutRequest := httptest.NewRequest(http.MethodDelete, "/sessions/current", nil)
			logoutRequest.AddCookie(cookie)
			logoutRequest.Header.Set("Origin", "https://gateway.example")
			logoutRequest.Header.Set(CSRFHeaderName, issued.CSRFToken)
			logout := httptest.NewRecorder()
			serveHTTP(handler, logout, logoutRequest)
			if logout.Code != http.StatusNoContent || len(logout.Result().Cookies()) != 1 || logout.Result().Cookies()[0].MaxAge != -1 {
				t.Fatalf("logout status=%d headers=%v body=%s", logout.Code, logout.Header(), logout.Body.String())
			}
			statusAfterLogout := httptest.NewRecorder()
			serveHTTP(handler, statusAfterLogout, statusRequest)
			if statusAfterLogout.Code != http.StatusUnauthorized {
				t.Fatalf("status after logout=%d body=%s", statusAfterLogout.Code, statusAfterLogout.Body.String())
			}
			revocations, err := store.Audit().List(context.Background(), storage.AuditFilter{Action: "admin.session.revoke"})
			if err != nil || len(revocations) != 1 || revocations[0].Outcome != "success" {
				t.Fatalf("logout audit=%+v error=%v", revocations, err)
			}
		})
	}
}

func TestManagementUIIsPublicButStrictlySandboxed(t *testing.T) {
	handler, _ := newPrincipalTokenHandler(t, false)
	request := httptest.NewRequest(http.MethodGet, "/ui", nil)
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "KubeLoop Control") ||
		!strings.Contains(recorder.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("UI status=%d CSP=%q body=%s", recorder.Code, recorder.Header().Get("Content-Security-Policy"), recorder.Body.String())
	}
}

func TestGatewayTokenExchangeRejectsMalformedBearerAndCrossSite(t *testing.T) {
	handler, store := newPrincipalTokenHandler(t, false)
	for _, mutate := range []func(*http.Request){
		func(request *http.Request) { request.Header.Set("Authorization", "Basic invalid") },
		func(request *http.Request) { request.Header.Set("Origin", "https://attacker.example") },
	} {
		request := httptest.NewRequest(http.MethodPost, "/sessions/token", bytes.NewBufferString(`{}`))
		request.RemoteAddr = "192.0.2.31:5000"
		request.Header.Set("Origin", "https://gateway.example")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer valid-access-token")
		mutate(request)
		recorder := httptest.NewRecorder()
		serveHTTP(handler, recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("rejected exchange status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{
		Action: "admin.session.principal.exchange",
	})
	if err != nil || len(events) != 2 || events[0].Outcome != "failure" || events[1].Outcome != "failure" {
		t.Fatalf("failure audit=%+v error=%v", events, err)
	}
}

func TestGatewayTokenExchangeRejectsNonEmptyOrNullJSONAndAudits(t *testing.T) {
	handler, store := newPrincipalTokenHandler(t, false)
	for _, body := range []string{`null`, `{"unexpected":true}`, `{`} {
		request := httptest.NewRequest(http.MethodPost, "/sessions/token", bytes.NewBufferString(body))
		request.RemoteAddr = "192.0.2.32:5000"
		request.Header.Set("Origin", "https://gateway.example")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer valid-access-token")
		recorder := httptest.NewRecorder()
		serveHTTP(handler, recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
	events, err := store.Audit().List(context.Background(), storage.AuditFilter{Action: "admin.session.principal.exchange"})
	if err != nil || len(events) != 3 {
		t.Fatalf("malformed exchange audit=%+v error=%v", events, err)
	}
	for _, event := range events {
		if event.Outcome != "failure" || strings.Contains(string(event.Metadata), "valid-access-token") {
			t.Fatalf("malformed exchange event=%+v", event)
		}
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
	verifier := &testVerifier{enabled: enabled, generation: generation}
	sessions, err := adminsession.New(store, verifier)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(config, sessions)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func newReadTestHandler(t *testing.T, authorizeBreakGlass bool) (*Handler, *storage.Store) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	generation := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	verifier := &testVerifier{enabled: true, generation: generation}
	sessions, err := adminsession.New(store, verifier)
	if err != nil {
		t.Fatal(err)
	}
	var authorizer *adminauthorization.Engine
	if authorizeBreakGlass {
		authorizer, err = adminauthorization.NewDenyAll(adminauthorization.WithBreakGlass(verifier))
	} else {
		authorizer, err = adminauthorization.NewDenyAll()
	}
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(
		Config{PublicURL: "https://gateway.example"}, sessions,
		WithReadAPI(authorizer, store, BuildInfo{
			Version: "v2-test", Commit: "test-commit", ProtocolMin: "2.0", ProtocolMax: "2.0",
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

type tokenAuthenticatorStub struct{ identity admintoken.AccessIdentity }

func (authenticator tokenAuthenticatorStub) Authenticate(_ context.Context, value string) (admintoken.AccessIdentity, error) {
	if value != "valid-access-token" {
		return admintoken.AccessIdentity{}, errors.New("invalid token")
	}
	return authenticator.identity, nil
}

type bootstrapStateStub struct{}

func (bootstrapStateStub) BootstrapRetired(context.Context) (bool, error) { return false, nil }

func newPrincipalTokenHandler(t *testing.T, bootstrap bool, extraOptions ...Option) (*Handler, *storage.Store) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	principal, err := store.Principals().Upsert(context.Background(), storage.Principal{
		ID: uuid.NewString(), Provider: "oidc", ExternalID: uuid.NewString(), Groups: []string{"platform"},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	family := storage.TokenFamily{
		ID: uuid.NewString(), PrincipalID: principal.ID, DeviceID: "browser",
		RefreshTokenHash: bytes.Repeat([]byte{22}, 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.TokenFamilies().Create(context.Background(), family); err != nil {
		t.Fatal(err)
	}
	sessions, err := adminsession.New(store)
	if err != nil {
		t.Fatal(err)
	}
	var authorizer *adminauthorization.Engine
	if bootstrap {
		authorizer, err = adminauthorization.NewDenyAll(adminauthorization.WithBootstrap(
			adminauthorization.BootstrapConfig{Groups: []string{"platform"}}, bootstrapStateStub{},
		))
	} else {
		authorizer, err = adminauthorization.New(adminauthorization.Snapshot{
			Version: adminauthorization.CurrentVersion, Revision: 1, Assignments: []adminauthorization.Assignment{{
				ID: uuid.NewString(), Role: adminauthorization.RolePlatformAdmin, Subjects: []string{principal.ID},
			}},
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	options := []Option{
		WithReadAPI(authorizer, store, BuildInfo{
			Version: "v2-test", Commit: "test", ProtocolMin: "2.0", ProtocolMax: "2.0",
		}),
		WithTokenExchange(tokenAuthenticatorStub{identity: admintoken.AccessIdentity{
			Principal: principal, FamilyID: family.ID, DeviceID: family.DeviceID, AccessExpiresAt: now.Add(5 * time.Minute),
		}}),
	}
	options = append(options, extraOptions...)
	handler, err := New(Config{PublicURL: "https://gateway.example"}, sessions, options...)
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

func performExchange(handler *Handler, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, exchangeRequest(body))
	return recorder
}

func issueLegacySession(t *testing.T, handler *Handler) (*http.Cookie, string) {
	t.Helper()
	issued, err := handler.sessions.ExchangeBreakGlass(
		context.Background(), netip.MustParseAddr("192.0.2.20"), []byte("valid"), uuid.NewString(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: SessionCookieName, Value: issued.SessionToken}, issued.CSRFToken
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
