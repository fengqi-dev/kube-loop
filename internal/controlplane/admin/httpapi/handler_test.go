package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	adminauthentication "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authentication"
	adminsession "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/session"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func TestAuthenticatedIdentityCanReadManagementAPI(t *testing.T) {
	handler, _ := newReadTestHandler(t)
	unauthenticated := httptest.NewRecorder()
	serveHTTP(handler, unauthenticated, httptest.NewRequest(http.MethodGet, "/bootstrap", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	cookie, _ := issueTestSession(t, handler)
	request := httptest.NewRequest(http.MethodGet, "/bootstrap", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	serveHTTP(handler, recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthenticatedWritesRequireCSRF(t *testing.T) {
	handler, _ := newReadTestHandler(t)
	cookie, csrf := issueTestSession(t, handler)
	server := echo.New()
	server.POST("/write", func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) }, handler.readAPI.authenticate)

	missing := httptest.NewRequest(http.MethodPost, "/write", nil)
	missing.AddCookie(cookie)
	missingRecorder := httptest.NewRecorder()
	server.ServeHTTP(missingRecorder, missing)
	if missingRecorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", missingRecorder.Code)
	}

	valid := httptest.NewRequest(http.MethodPost, "/write", nil)
	valid.AddCookie(cookie)
	valid.Header.Set(CSRFHeaderName, csrf)
	validRecorder := httptest.NewRecorder()
	server.ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF status = %d body=%s", validRecorder.Code, validRecorder.Body.String())
	}
}

func TestManagementCookieSecurityMatchesPublicURL(t *testing.T) {
	httpsHandler, _ := newReadTestHandler(t)
	httpHandler, err := New(Config{PublicURL: "http://gateway.example"}, httpsHandler.sessions)
	if err != nil {
		t.Fatal(err)
	}
	issued := adminsession.Credentials{SessionToken: "session", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}

	tests := []struct {
		name        string
		handler     *Handler
		cookieNames []string
		secure      bool
	}{
		{name: "HTTPS", handler: httpsHandler, cookieNames: []string{SessionCookieName, CSRFCookieName}, secure: true},
		{name: "HTTP", handler: httpHandler, cookieNames: []string{httpSessionCookieName, httpCSRFCookieName}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx := echo.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), recorder)
			test.handler.setSessionCookies(ctx, issued)
			cookies := recorder.Result().Cookies()
			if len(cookies) != 2 {
				t.Fatalf("cookie count = %d", len(cookies))
			}
			for index, cookie := range cookies {
				if cookie.Name != test.cookieNames[index] || cookie.Secure != test.secure {
					t.Fatalf("cookie[%d] = %#v", index, cookie)
				}
			}
		})
	}
}

func newReadTestHandler(t *testing.T, extraOptions ...Option) (*Handler, *storage.Store) {
	t.Helper()
	store, err := storage.Open(context.Background(), storage.Config{Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "management.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if _, err := store.Identities().Create(context.Background(), storage.Identity{ID: testManagementIdentityID, Type: "human", DisplayName: "Test user", Status: "active", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.OAuthSessions().Create(context.Background(), storage.OAuthSession{Kind: "refresh_token", SignatureHash: bytes.Repeat([]byte{31}, 32), RequestID: testManagementAuthorizationID, IdentityID: testManagementIdentityID, RequestJSON: []byte(`{}`), Status: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	sessions, err := adminsession.New(store)
	if err != nil {
		t.Fatal(err)
	}
	options := []Option{WithReadAPI(store)}
	options = append(options, extraOptions...)
	handler, err := New(Config{PublicURL: "https://gateway.example"}, sessions, options...)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func issueTestSession(t *testing.T, handler *Handler) (*http.Cookie, string) {
	t.Helper()
	issued, err := handler.sessions.ExchangeIdentity(context.Background(), testManagementIdentityID, testManagementAuthorizationID, adminauthentication.Normal, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: SessionCookieName, Value: issued.SessionToken}, issued.CSRFToken
}

const (
	testManagementIdentityID      = "9fb7f23a-4ce5-4aa0-bdc6-55551b4a1b89"
	testManagementAuthorizationID = "e412bbde-546f-42c8-8221-47a744e18af8"
)
