package controller

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

	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
)

type recordingAuthorizer struct {
	subject  authorization.Subject
	request  authorization.Request
	calls    int
	decision authorization.Decision
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
	server := newAPITestServer(t)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/clusters", nil))
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
	if document.Error.Code != CodeUnauthenticated || document.Error.RequestID == "" {
		t.Fatalf("unexpected error: %#v", document.Error)
	}
	if document.Error.RequestID != response.Header().Get(RequestIDHeader) {
		t.Fatalf("body request ID %q differs from header %q", document.Error.RequestID, response.Header().Get(RequestIDHeader))
	}
}

func TestAPIProvidesPrincipalRequestIDAndDeadline(t *testing.T) {
	var handled bool
	server := newAPITestServer(t,
		WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{Subject: "user-123"}, nil
		})),
		WithAPIHandler(APIHandlerFunc(func(writer http.ResponseWriter, request *http.Request, principal Principal) *APIError {
			handled = true
			if principal.Subject != "user-123" {
				t.Fatalf("subject = %q", principal.Subject)
			}
			if RequestIDFromContext(request.Context()) == "" {
				t.Fatal("request ID missing from context")
			}
			deadline, ok := request.Context().Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > DefaultAPIRequestTimeout {
				t.Fatalf("unexpected request deadline: %v, %t", deadline, ok)
			}
			writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
			return nil
		})),
		WithAuthorizer(allowAllAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix, nil))
	if !handled || response.Code != http.StatusOK {
		t.Fatalf("handled = %t, status = %d", handled, response.Code)
	}
}

func TestAPIBodyLimitAndStrictJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test", MaxRequestBodyBytes: 16},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{Subject: "user-123"}, nil
		})),
		WithAPIHandler(APIHandlerFunc(func(_ http.ResponseWriter, request *http.Request, _ Principal) *APIError {
			var body input
			return DecodeJSON(request, &body)
		})),
		WithAuthorizer(allowAllAuthorizer(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		contentType string
		body        string
		message     string
	}{
		{name: "too large", contentType: "application/json", body: `{"name":"0123456789"}`, message: "request body exceeds the size limit"},
		{name: "unknown field", contentType: "application/json", body: `{"other":1}`, message: "request body contains invalid JSON"},
		{name: "multiple documents", contentType: "application/json", body: `{}` + "\n" + `{}`, message: "request body must contain one JSON document"},
		{name: "wrong content type", contentType: "text/plain", body: `{}`, message: "Content-Type must be application/json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, APIPathPrefix+"/resource", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
			document := decodeAPIError(t, response)
			if document.Error.Code != CodeInvalidArgument || document.Error.Message != test.message {
				t.Fatalf("unexpected error: %#v", document.Error)
			}
		})
	}
}

func TestAPIRecoversPanicWithoutLeakingIt(t *testing.T) {
	server := newAPITestServer(t,
		WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{Subject: "user-123"}, nil
		})),
		WithAPIHandler(APIHandlerFunc(func(http.ResponseWriter, *http.Request, Principal) *APIError {
			panic("secret panic detail")
		})),
		WithAuthorizer(allowAllAuthorizer(t)),
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret panic detail") {
		t.Fatal("panic detail leaked to client")
	}
	if document := decodeAPIError(t, response); document.Error.Code != CodeInternal {
		t.Fatalf("code = %q", document.Error.Code)
	}
}

func TestAPIDefaultPolicyDeniesBeforeHandlerWithoutLeakingResourceExistence(t *testing.T) {
	handled := false
	server := newAPITestServer(t,
		WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{Subject: "user-123", Groups: []string{"developers"}}, nil
		})),
		WithAPIHandler(APIHandlerFunc(func(http.ResponseWriter, *http.Request, Principal) *APIError {
			handled = true
			return nil
		})),
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/namespaces/secret/pods/existing-pod", nil))
	if handled || response.Code != http.StatusForbidden {
		t.Fatalf("handled = %t, status = %d", handled, response.Code)
	}
	document := decodeAPIError(t, response)
	if document.Error.Code != CodeForbidden || document.Error.Message != "operation is not permitted" {
		t.Fatalf("error = %#v", document.Error)
	}
}

func TestAPIMapsRequestToPolicyAndStoresDecisionInContext(t *testing.T) {
	engine, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "payments-read", Groups: []string{"developers"}, Namespaces: []string{"payments"},
		Operations: []string{"get"}, ResourceKinds: []string{"pods"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server := newAPITestServer(t,
		WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{Subject: "user-123", Groups: []string{"developers"}}, nil
		})),
		WithAuthorizer(engine),
		WithAPIHandler(APIHandlerFunc(func(writer http.ResponseWriter, request *http.Request, _ Principal) *APIError {
			authorizationRequest, decision, ok := AuthorizationFromContext(request.Context())
			if !ok || authorizationRequest.Operation != "get" || authorizationRequest.Namespace != "payments" ||
				authorizationRequest.ResourceKind != "pods" || authorizationRequest.ResourceName != "pod-1" || decision.RuleID != "payments-read" {
				t.Fatalf("authorization context = %#v, %#v, %t", authorizationRequest, decision, ok)
			}
			writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
			return nil
		})),
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/namespaces/payments/pods/pod-1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUnifiedAuthorizerGatesTaskCreationAndWebSocketStreams(t *testing.T) {
	tests := []struct {
		name, method, path, operation, resourceKind string
	}{
		{"relay ticket", http.MethodPost, APIPathPrefix + "/sessions/session-1/tickets?namespace=development", "create", "relay-tickets"},
		{"port forward", http.MethodPost, APIPathPrefix + "/sessions/session-1/port-forwards?namespace=development", "create", "port-forwards"},
		{"pod exec", http.MethodPost, APIPathPrefix + "/sessions/session-1/exec?namespace=development", "create", "pod-exec"},
		{"pod exec stream", http.MethodGet, APIPathPrefix + "/sessions/session-1/exec/task-1/stream?namespace=development", "stream", "pod-exec"},
		{"file stream", http.MethodGet, APIPathPrefix + "/sessions/session-1/file-transfers/task-1/stream?namespace=development", "stream", "file-transfers"},
		{"exchange stream", http.MethodGet, APIPathPrefix + "/sessions/session-1/exchanges/task-1/stream?namespace=development", "stream", "exchanges"},
		{"mirror stream", http.MethodGet, APIPathPrefix + "/sessions/session-1/mirrors/task-1/stream?namespace=development", "stream", "mirrors"},
		{"preview stream", http.MethodGet, APIPathPrefix + "/sessions/session-1/previews/task-1/stream?namespace=development", "stream", "previews"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handled := false
			authorizer := &recordingAuthorizer{decision: authorization.Decision{Allowed: true, RuleID: "unified"}}
			server := newAPITestServer(t,
				WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
					return Principal{Subject: "principal-1", Groups: []string{"developers"}}, nil
				})),
				WithAuthorizer(authorizer),
				WithAPIHandler(APIHandlerFunc(func(writer http.ResponseWriter, _ *http.Request, _ Principal) *APIError {
					handled = true
					writer.WriteHeader(http.StatusNoContent)
					return nil
				})),
			)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if !handled || response.Code != http.StatusNoContent || authorizer.calls != 1 {
				t.Fatalf("handled = %t, status = %d, authorizer calls = %d", handled, response.Code, authorizer.calls)
			}
			if authorizer.subject.ID != "principal-1" || authorizer.request.Operation != test.operation ||
				authorizer.request.Namespace != "development" || authorizer.request.ResourceKind != test.resourceKind {
				t.Fatalf("subject = %#v, request = %#v", authorizer.subject, authorizer.request)
			}
		})
	}
}

func TestAPIErrorStatusMapping(t *testing.T) {
	tests := map[ErrorCode]int{
		CodeUnauthenticated: http.StatusUnauthorized,
		CodeForbidden:       http.StatusForbidden,
		CodeNotFound:        http.StatusNotFound,
		CodeConflict:        http.StatusConflict,
		CodeInvalidArgument: http.StatusBadRequest,
		CodeUnavailable:     http.StatusServiceUnavailable,
		CodeVersionMismatch: http.StatusUpgradeRequired,
		CodeRateLimited:     http.StatusTooManyRequests,
		CodeInternal:        http.StatusInternalServerError,
	}
	for code, expectedStatus := range tests {
		response := httptest.NewRecorder()
		writeAPIError(response, "request-id", &APIError{Code: code})
		if response.Code != expectedStatus {
			t.Errorf("%s status = %d, want %d", code, response.Code, expectedStatus)
		}
	}
}

func TestWebSocketUpgradeDetectionIsStrict(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v2/sessions/id/exec/id/stream", nil)
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
	engine, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "test-allow-all", Subjects: []string{"*"}, Namespaces: []string{"*"},
		Operations: []string{"*"}, ResourceKinds: []string{"*"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
