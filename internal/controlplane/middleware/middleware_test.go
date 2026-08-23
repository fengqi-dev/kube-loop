package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

type recordingAuthorizer struct{ calls int }

func (authorizer *recordingAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	authorizer.calls++
	return authorization.Decision{Allowed: true}
}

type fixedAuthorizer struct {
	allowed bool
	calls   int
}

type recordingAuditSink struct {
	records []AuditRecord
}

func (sink *recordingAuditSink) Record(_ context.Context, record AuditRecord) error {
	sink.records = append(sink.records, record)
	return nil
}

func (authorizer *fixedAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	authorizer.calls++
	return authorization.Decision{Allowed: authorizer.allowed}
}

func TestErrorStatusMapping(t *testing.T) {
	tests := map[controlplaneapi.ErrorCode]int{
		controlplaneapi.CodeUnauthenticated: http.StatusUnauthorized,
		controlplaneapi.CodeForbidden:       http.StatusForbidden,
		controlplaneapi.CodeNotFound:        http.StatusNotFound,
		controlplaneapi.CodeConflict:        http.StatusConflict,
		controlplaneapi.CodeInvalidArgument: http.StatusBadRequest,
		controlplaneapi.CodeUnavailable:     http.StatusServiceUnavailable,
		controlplaneapi.CodeVersionMismatch: http.StatusUpgradeRequired,
		controlplaneapi.CodeRateLimited:     http.StatusTooManyRequests,
		controlplaneapi.CodeInternal:        http.StatusInternalServerError,
	}
	for code, expectedStatus := range tests {
		response := httptest.NewRecorder()
		ctx := echo.New().
			NewContext(httptest.NewRequest(http.MethodGet, "/", nil), response)
		writeError(ctx, "request-id", &controlplaneapi.Error{Code: code})
		if response.Code != expectedStatus {
			t.Errorf(
				"%s status = %d, want %d",
				code,
				response.Code,
				expectedStatus,
			)
		}
	}
}

func TestWebSocketUpgradeDetectionIsStrict(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/sessions/id/exec/id/stream",
		nil,
	)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	if !isWebSocketUpgrade(request) {
		t.Fatal("valid WebSocket upgrade was not detected")
	}
	request.Method = http.MethodPost
	if isWebSocketUpgrade(request) {
		t.Fatal("non-GET request bypassed the API timeout")
	}
}

func TestAuthenticatedVersionDiscoveryIsAllowed(t *testing.T) {
	authorizer := &recordingAuthorizer{}
	middleware := New(Config{
		APIPathPrefix:      "/api",
		RequestTimeout:     time.Second,
		MaxRequestBodySize: 1 << 20,
		Authenticator: controlplaneapi.AuthenticatorFunc(
			func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
				return controlplaneapi.Identity{
					Subject:  "81a678af-e99c-4411-99dc-57fd59d83189",
					Provider: "local",
				}, nil
			},
		),
		Authorizer: authorizer,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	response := httptest.NewRecorder()
	ctx := echo.New().NewContext(request, response)
	err := middleware(
		func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) },
	)(
		ctx,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("version status = %d", response.Code)
	}
	if authorizer.calls != 1 {
		t.Fatalf("version authorization calls = %d", authorizer.calls)
	}
}

func TestMiddlewareFailsClosedBeforeHandler(t *testing.T) {
	tests := []struct {
		name             string
		identity         controlplaneapi.Identity
		authError        *controlplaneapi.Error
		allowed          bool
		wantStatus       int
		wantAuthzCalls   int
		wantAuthenticate string
	}{
		{
			name: "authentication error",
			authError: &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required",
			},
			wantStatus: http.StatusUnauthorized, wantAuthenticate: "Bearer",
		},
		{
			name:       "empty authenticated subject",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "authorization denied",
			identity: controlplaneapi.Identity{
				Subject: "identity-1", Provider: "local",
			},
			wantStatus: http.StatusForbidden, wantAuthzCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authorizer := &fixedAuthorizer{allowed: test.allowed}
			handlerCalls := 0
			middleware := New(Config{
				APIPathPrefix: "/api", RequestTimeout: time.Second, MaxRequestBodySize: 1 << 20,
				Authenticator: controlplaneapi.AuthenticatorFunc(
					func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
						return test.identity, test.authError
					},
				),
				Authorizer: authorizer,
			})
			request := httptest.NewRequest(http.MethodGet, "/api/version", nil)
			response := httptest.NewRecorder()
			ctx := echo.New().NewContext(request, response)

			if err := middleware(func(*echo.Context) error {
				handlerCalls++
				return nil
			})(ctx); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.wantStatus || authorizer.calls != test.wantAuthzCalls ||
				handlerCalls != 0 || response.Header().Get("WWW-Authenticate") != test.wantAuthenticate {
				t.Fatalf(
					"response=%d authz=%d handler=%d authenticate=%q",
					response.Code,
					authorizer.calls,
					handlerCalls,
					response.Header().Get("WWW-Authenticate"),
				)
			}
		})
	}
}

func TestMiddlewareAuditsAccessOutcomes(t *testing.T) {
	tests := []struct {
		name          string
		identity      controlplaneapi.Identity
		authError     *controlplaneapi.Error
		allowed       bool
		handlerStatus int
		wantStatus    int
		wantOutcome   string
	}{
		{
			name: "success", identity: controlplaneapi.Identity{Subject: "identity-1"},
			allowed: true, handlerStatus: http.StatusNoContent,
			wantStatus: http.StatusNoContent, wantOutcome: "success",
		},
		{
			name: "denied", identity: controlplaneapi.Identity{Subject: "identity-1"},
			wantStatus: http.StatusForbidden, wantOutcome: "denied",
		},
		{
			name: "unauthenticated",
			authError: &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required",
			},
			wantStatus: http.StatusUnauthorized, wantOutcome: "unauthenticated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audit := &recordingAuditSink{}
			authorizer := &fixedAuthorizer{allowed: test.allowed}
			middleware := New(Config{
				APIPathPrefix: "/api", RequestTimeout: time.Second, MaxRequestBodySize: 1 << 20,
				Authenticator: controlplaneapi.AuthenticatorFunc(
					func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
						return test.identity, test.authError
					},
				),
				Authorizer: authorizer, Audit: audit,
			})
			request := httptest.NewRequest(http.MethodGet, "/api/version", nil)
			response := httptest.NewRecorder()
			response.Header().Set(echo.HeaderXRequestID, "request-1")
			ctx := echo.New().NewContext(request, response)

			if err := middleware(func(ctx *echo.Context) error {
				return ctx.NoContent(test.handlerStatus)
			})(ctx); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.wantStatus || len(audit.records) != 1 {
				t.Fatalf("response=%d audit=%#v", response.Code, audit.records)
			}
			record := audit.records[0]
			if record.RequestID != "request-1" || record.IdentityID != test.identity.Subject ||
				record.Operation != operationList || record.ResourceKind != "version" ||
				record.HTTPStatus != test.wantStatus || record.Outcome != test.wantOutcome || record.Duration < 0 {
				t.Fatalf("audit record = %#v", record)
			}
		})
	}
}
