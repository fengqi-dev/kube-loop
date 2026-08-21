package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
)

type recordingAuthorizer struct {
	subject  authorization.Subject
	request  authorization.Request
	calls    int
	decision authorization.Decision
}

type errorEnvelope struct {
	Error errorDocument `json:"error"`
}

type errorDocument struct {
	Code      controlplaneapi.ErrorCode `json:"code"`
	Message   string                    `json:"message"`
	Field     string                    `json:"field,omitempty"`
	RequestID string                    `json:"requestId"`
}

func (authorizer *recordingAuthorizer) Authorize(
	_ context.Context,
	subject authorization.Subject,
	request authorization.Request,
) authorization.Decision {
	authorizer.calls++
	authorizer.subject = subject
	authorizer.request = request
	return authorizer.decision
}

func TestAPIDefaultsToAuthenticationRequired(t *testing.T) {
	server := newAPITestServer(
		t,
		WithAPIRoutes(
			testEndpoint(
				func(http.ResponseWriter, *http.Request, controlplaneapi.Identity) *controlplaneapi.Error {
					t.Fatal("unauthenticated request reached API endpoint")
					return nil
				},
			),
		),
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, APIPathPrefix+"/clusters", nil)
	request.Header.Set(echo.HeaderXRequestID, "33333333-3333-4333-8333-333333333333")
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q", response.Header().Get("WWW-Authenticate"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	document := decodeAPIError(t, response)
	if document.Error.Code != controlplaneapi.CodeUnauthenticated ||
		document.Error.RequestID == "" {
		t.Fatalf("unexpected error: %#v", document.Error)
	}
	if document.Error.RequestID != response.Header().Get(echo.HeaderXRequestID) {
		t.Fatalf(
			"body request ID %q differs from header %q",
			document.Error.RequestID,
			response.Header().Get(echo.HeaderXRequestID),
		)
	}
	if document.Error.RequestID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("request ID = %q, want client-provided ID", document.Error.RequestID)
	}
}

func TestAPIProvidesIdentityRequestIDAndDeadline(t *testing.T) {
	var handled bool
	server := newAPITestServer(
		t,
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "user-123"}, nil
				},
			),
		),
		WithAPIRoutes(
			testEndpoint(
				func(writer http.ResponseWriter, request *http.Request, identity controlplaneapi.Identity) *controlplaneapi.Error {
					handled = true
					if identity.Subject != "user-123" {
						t.Fatalf("subject = %q", identity.Subject)
					}
					if controlplanemiddleware.RequestIDFromContext(request.Context()) == "" {
						t.Fatal("request ID missing from context")
					}
					deadline, ok := request.Context().Deadline()
					if !ok || time.Until(deadline) <= 0 ||
						time.Until(deadline) > DefaultAPIRequestTimeout {
						t.Fatalf("unexpected request deadline: %v, %t", deadline, ok)
					}
					writeTestJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
					return nil
				},
			),
		),
		WithAuthorizer(allowAllAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix, nil))
	if !handled || response.Code != http.StatusOK {
		t.Fatalf("handled = %t, status = %d", handled, response.Code)
	}
}

func TestAPIBodyLimitAndEchoBinding(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test", MaxRequestBodyBytes: 16},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "user-123"}, nil
				},
			),
		),
		WithAPIRoutes(
			testEndpoint(
				EndpointFunc(
					func(ctx *echo.Context, _ controlplaneapi.Identity) *controlplaneapi.Error {
						var body input
						if err := ctx.Bind(&body); err != nil {
							return controlplanemiddleware.BindingError(err)
						}
						return nil
					},
				),
			),
		),
		WithAuthorizer(allowAllAuthorizer(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		message     string
	}{
		{
			name:        "too large",
			contentType: "application/json",
			body:        `{"name":"0123456789"}`,
			status:      http.StatusBadRequest,
			message:     "request body exceeds the size limit",
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"other":1}`,
			status:      http.StatusOK,
		},
		{
			name:        "multiple documents",
			contentType: "application/json",
			body:        `{}` + "\n" + `{}`,
			status:      http.StatusBadRequest,
			message:     "request binding failed",
		},
		{
			name:        "wrong content type",
			contentType: "text/plain",
			body:        `{}`,
			status:      http.StatusBadRequest,
			message:     "request binding failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				APIPathPrefix+"/resource",
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.status == http.StatusOK {
				return
			}
			document := decodeAPIError(t, response)
			if document.Error.Code != controlplaneapi.CodeInvalidArgument ||
				document.Error.Message != test.message {
				t.Fatalf("unexpected error: %#v", document.Error)
			}
		})
	}
}

func TestAPIRecoversPanicWithoutLeakingIt(t *testing.T) {
	server := newAPITestServer(
		t,
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "user-123"}, nil
				},
			),
		),
		WithAPIRoutes(
			testEndpoint(
				func(http.ResponseWriter, *http.Request, controlplaneapi.Identity) *controlplaneapi.Error {
					panic("secret panic detail")
				},
			),
		),
		WithAuthorizer(allowAllAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret panic detail") {
		t.Fatal("panic detail leaked to client")
	}
	if document := decodeAPIError(t, response); document.Error.Code != controlplaneapi.CodeInternal {
		t.Fatalf("code = %q", document.Error.Code)
	}
}

func TestAPIDefaultAllowsAuthenticatedIdentity(t *testing.T) {
	handled := false
	server := newAPITestServer(
		t,
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{
						Subject: "user-123",
						Groups:  []string{"developers"},
					}, nil
				},
			),
		),
		WithAPIRoutes(
			testEndpoint(
				func(http.ResponseWriter, *http.Request, controlplaneapi.Identity) *controlplaneapi.Error {
					handled = true
					return nil
				},
			),
		),
	)
	response := httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/namespaces/secret/pods/existing-pod", nil))
	if !handled || response.Code != http.StatusOK {
		t.Fatalf("handled = %t, status = %d", handled, response.Code)
	}
}

func TestAPIMapsRequestAndStoresAuthenticationDecisionInContext(t *testing.T) {
	engine := authorization.NewAuthenticated()
	server := newAPITestServer(
		t,
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{
						Subject: "user-123",
						Groups:  []string{"developers"},
					}, nil
				},
			),
		),
		WithAuthorizer(engine),
		WithAPIRoutes(
			testEndpoint(
				func(writer http.ResponseWriter, request *http.Request, _ controlplaneapi.Identity) *controlplaneapi.Error {
					authorizationRequest, decision, ok := controlplanemiddleware.AuthorizationFromContext(
						request.Context(),
					)
					if !ok || authorizationRequest.Operation != "get" || authorizationRequest.Namespace != "payments" ||
						authorizationRequest.ResourceKind != "pods" ||
						authorizationRequest.ResourceName != "pod-1" ||
						!decision.Allowed {
						t.Fatalf(
							"authorization context = %#v, %#v, %t",
							authorizationRequest,
							decision,
							ok,
						)
					}
					writeTestJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
					return nil
				},
			),
		),
	)
	response := httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/namespaces/payments/pods/pod-1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUnifiedAuthorizerGatesTaskCreationAndControlPlaneStreams(t *testing.T) {
	tests := []struct {
		name, method, path, operation, resourceKind string
	}{
		{
			"relay ticket",
			http.MethodPost,
			APIPathPrefix + "/sessions/session-1/tickets?namespace=development",
			"create",
			"relay-tickets",
		},
		{
			"port forward",
			http.MethodPost,
			APIPathPrefix + "/sessions/session-1/port-forwards?namespace=development",
			"create",
			"port-forwards",
		},
		{
			"pod exec",
			http.MethodPost,
			APIPathPrefix + "/sessions/session-1/exec?namespace=development",
			"create",
			"pod-exec",
		},
		{
			"pod exec stream",
			http.MethodGet,
			APIPathPrefix + "/sessions/session-1/exec/task-1/stream?namespace=development",
			"stream",
			"pod-exec",
		},
		{
			"file stream",
			http.MethodGet,
			APIPathPrefix + "/sessions/session-1/file-transfers/task-1/stream?namespace=development",
			"stream",
			"file-transfers",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handled := false
			authorizer := &recordingAuthorizer{decision: authorization.Decision{Allowed: true}}
			server := newAPITestServer(
				t,
				WithAuthenticator(
					controlplaneapi.AuthenticatorFunc(
						func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
							return controlplaneapi.Identity{
								Subject: "identity-1",
								Groups:  []string{"developers"},
							}, nil
						},
					),
				),
				WithAuthorizer(authorizer),
				WithAPIRoutes(
					sessionTestEndpoint(
						func(writer http.ResponseWriter, _ *http.Request, _ controlplaneapi.Identity) *controlplaneapi.Error {
							handled = true
							writer.WriteHeader(http.StatusNoContent)
							return nil
						},
					),
				),
			)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if !handled || response.Code != http.StatusNoContent || authorizer.calls != 1 {
				t.Fatalf(
					"handled = %t, status = %d, authorizer calls = %d",
					handled,
					response.Code,
					authorizer.calls,
				)
			}
			if authorizer.subject.ID != "identity-1" ||
				authorizer.request.Operation != test.operation ||
				authorizer.request.Namespace != "development" ||
				authorizer.request.ResourceKind != test.resourceKind {
				t.Fatalf("subject = %#v, request = %#v", authorizer.subject, authorizer.request)
			}
		})
	}
}

func TestSessionRoutesDoNotUseKubeloopPrefix(t *testing.T) {
	handled := false
	server := newAPITestServer(
		t,
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "user-123"}, nil
				},
			),
		),
		WithAuthorizer(allowAllAuthorizer(t)),
		WithAPIRoutes(
			APIRoutes{
				Sessions: SessionEndpoints{
					Create: func(_ *echo.Context, _ controlplaneapi.Identity) *controlplaneapi.Error {
						handled = true
						return nil
					},
				},
			},
		),
	)

	response := httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/sessions?namespace=development", nil))
	if !handled || response.Code != http.StatusOK {
		t.Fatalf("new route handled = %t, status = %d", handled, response.Code)
	}

	handled = false
	response = httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/kubeloop/api/sessions?namespace=development", nil))
	if handled || response.Code != http.StatusNotFound {
		t.Fatalf("legacy route handled = %t, status = %d", handled, response.Code)
	}
}

func newAPITestServer(t *testing.T, options ...ServerOption) *Server {
	t.Helper()
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test"},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		options...,
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func decodeAPIError(t *testing.T, response *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var document errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func allowAllAuthorizer(t *testing.T) authorization.Authorizer {
	t.Helper()
	return authorization.NewAuthenticated()
}
