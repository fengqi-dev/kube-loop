package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/go-chi/chi/v5"
)

func TestAPIRouterExactChiRouteTakesPriorityOverPrefixMount(t *testing.T) {
	router := NewAPIRouter()
	exactCalled := false
	if err := router.Handle(http.MethodPost, APIPathPrefix+"/sessions/{sessionID}/tickets", APIHandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
		_ Principal,
	) *APIError {
		exactCalled = true
		if got := chi.URLParam(request, "sessionID"); got != "session-1" {
			t.Fatalf("sessionID = %q", got)
		}
		writer.WriteHeader(http.StatusCreated)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := router.HandlePrefix(APIPathPrefix+"/sessions", APIHandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
		Principal,
	) *APIError {
		t.Fatal("prefix handler received exact ticket route")
		return nil
	})); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	apiError := router.ServeAPI(response, authorizedRouterRequest(httptest.NewRequest(
		http.MethodPost, APIPathPrefix+"/sessions/session-1/tickets", nil,
	)), Principal{Subject: "principal-1"})
	if apiError != nil || !exactCalled || response.Code != http.StatusCreated {
		t.Fatalf("error = %v, exactCalled = %t, status = %d", apiError, exactCalled, response.Code)
	}
}

func TestAPIRouterDispatchesMountedPrefixesWithPrincipal(t *testing.T) {
	router := NewAPIRouter()
	var receivedPath string
	if err := router.HandlePrefix(APIPathPrefix+"/sessions", APIHandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
		principal Principal,
	) *APIError {
		receivedPath = request.URL.Path
		if principal.Subject != "principal-1" {
			t.Fatalf("principal subject = %q", principal.Subject)
		}
		writer.WriteHeader(http.StatusNoContent)
		return nil
	})); err != nil {
		t.Fatal(err)
	}

	request := authorizedRouterRequest(httptest.NewRequest(http.MethodPost, APIPathPrefix+"/sessions/id/heartbeat", nil))
	response := httptest.NewRecorder()
	if apiError := router.ServeAPI(response, request, Principal{Subject: "principal-1"}); apiError != nil {
		t.Fatalf("ServeAPI error = %v", apiError)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if receivedPath != APIPathPrefix+"/sessions/id/heartbeat" {
		t.Fatalf("handler path = %q", receivedPath)
	}
}

func TestAPIRouterResetsParentChiMountContext(t *testing.T) {
	apiRouter := NewAPIRouter()
	if err := apiRouter.HandlePrefix(APIPathPrefix+"/sessions", APIHandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
		_ Principal,
	) *APIError {
		writer.WriteHeader(http.StatusNoContent)
		return nil
	})); err != nil {
		t.Fatal(err)
	}

	outer := chi.NewRouter()
	outer.Mount(APIPathPrefix, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request = authorizedRouterRequest(request)
		if apiError := apiRouter.ServeAPI(writer, request, Principal{Subject: "principal-1"}); apiError != nil {
			writeAPIError(writer, "request-1", apiError)
		}
	}))
	response := httptest.NewRecorder()
	outer.ServeHTTP(response, httptest.NewRequest(http.MethodGet, APIPathPrefix+"/sessions/id", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAPIRouterFallbackAndPrefixBoundary(t *testing.T) {
	router := NewAPIRouter()
	if err := router.HandlePrefix(APIPathPrefix+"/sessions", APIHandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
		Principal,
	) *APIError {
		t.Fatal("sessions handler must not match a partial path segment")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	fallbackCalled := false
	router.SetFallback(APIHandlerFunc(func(http.ResponseWriter, *http.Request, Principal) *APIError {
		fallbackCalled = true
		return &APIError{Code: CodeNotFound, Message: "fallback"}
	}))

	apiError := router.ServeAPI(
		httptest.NewRecorder(),
		authorizedRouterRequest(httptest.NewRequest(http.MethodGet, APIPathPrefix+"/sessions-extra", nil)),
		Principal{Subject: "principal-1"},
	)
	if !fallbackCalled || apiError == nil || apiError.Message != "fallback" {
		t.Fatalf("fallbackCalled = %t, error = %#v", fallbackCalled, apiError)
	}
}

func TestAPIRouterRejectsFeatureDispatchWithoutAuthorizerProof(t *testing.T) {
	router := NewAPIRouter()
	handled := false
	if err := router.Handle(http.MethodPost, APIPathPrefix+"/sessions/{sessionID}/exec", APIHandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
		Principal,
	) *APIError {
		handled = true
		return nil
	})); err != nil {
		t.Fatal(err)
	}

	apiError := router.ServeAPI(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, APIPathPrefix+"/sessions/session-1/exec?namespace=development", nil),
		Principal{Subject: "principal-1"},
	)
	if handled || apiError == nil || apiError.Code != CodeForbidden {
		t.Fatalf("handled = %t, error = %#v", handled, apiError)
	}
}

func TestAPIRouterRejectsInvalidAndDuplicatePrefixes(t *testing.T) {
	router := NewAPIRouter()
	handler := APIHandlerFunc(func(http.ResponseWriter, *http.Request, Principal) *APIError { return nil })
	if err := router.HandlePrefix("/v1/sessions", handler); err == nil {
		t.Fatal("non-v2 prefix was accepted")
	}
	if err := router.HandlePrefix(APIPathPrefix+"/{resource}", handler); err == nil {
		t.Fatal("non-literal prefix was accepted")
	}
	if err := router.HandlePrefix(APIPathPrefix+"/sessions", handler); err != nil {
		t.Fatal(err)
	}
	if err := router.HandlePrefix(APIPathPrefix+"/sessions/", handler); err == nil {
		t.Fatal("duplicate normalized prefix was accepted")
	}
}

func TestRelayTicketAuthorizationMapping(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		APIPathPrefix+"/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development",
		nil,
	)
	mapped := authorizationRequestForHTTP(request)
	if mapped.Operation != "create" || mapped.ResourceKind != "relay-tickets" ||
		mapped.ResourceName != "33333333-3333-4333-8333-333333333333" || mapped.Namespace != "development" {
		t.Fatalf("authorization request = %#v", mapped)
	}
}

func TestFileTransferAuthorizationMapping(t *testing.T) {
	taskID := "44444444-4444-4444-8444-444444444444"
	tests := []struct {
		method, path, operation, resourceName string
	}{
		{http.MethodPost, APIPathPrefix + "/sessions/session-id/file-transfers?namespace=development", "create", "session-id"},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/file-transfers/" + taskID + "?namespace=development", "get", taskID},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/file-transfers/" + taskID + "/stream?namespace=development", "stream", taskID},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, nil)
		mapped := authorizationRequestForHTTP(request)
		if mapped.Operation != test.operation || mapped.ResourceKind != "file-transfers" ||
			mapped.ResourceName != test.resourceName || mapped.Namespace != "development" {
			t.Fatalf("%s %s authorization request = %#v", test.method, test.path, mapped)
		}
	}
}

func TestExchangeAuthorizationMapping(t *testing.T) {
	taskID := "66666666-6666-4666-8666-666666666666"
	tests := []struct {
		method, path, operation, resourceName string
	}{
		{http.MethodPost, APIPathPrefix + "/sessions/session-id/exchanges?namespace=development", "create", "session-id"},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/exchanges/" + taskID + "?namespace=development", "get", taskID},
		{http.MethodDelete, APIPathPrefix + "/sessions/session-id/exchanges/" + taskID + "?namespace=development", "delete", taskID},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/exchanges/" + taskID + "/stream?namespace=development", "stream", taskID},
	}
	for _, test := range tests {
		mapped := authorizationRequestForHTTP(httptest.NewRequest(test.method, test.path, nil))
		if mapped.Operation != test.operation || mapped.ResourceKind != "exchanges" ||
			mapped.ResourceName != test.resourceName || mapped.Namespace != "development" {
			t.Fatalf("%s %s authorization request = %#v", test.method, test.path, mapped)
		}
	}
}

func TestMirrorAuthorizationMapping(t *testing.T) {
	taskID := "77777777-7777-4777-8777-777777777777"
	tests := []struct {
		method, path, operation, resourceName string
	}{
		{http.MethodPost, APIPathPrefix + "/sessions/session-id/mirrors?namespace=development", "create", "session-id"},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/mirrors/" + taskID + "?namespace=development", "get", taskID},
		{http.MethodDelete, APIPathPrefix + "/sessions/session-id/mirrors/" + taskID + "?namespace=development", "delete", taskID},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/mirrors/" + taskID + "/stream?namespace=development", "stream", taskID},
	}
	for _, test := range tests {
		mapped := authorizationRequestForHTTP(httptest.NewRequest(test.method, test.path, nil))
		if mapped.Operation != test.operation || mapped.ResourceKind != "mirrors" ||
			mapped.ResourceName != test.resourceName || mapped.Namespace != "development" {
			t.Fatalf("%s %s authorization request = %#v", test.method, test.path, mapped)
		}
	}
}

func TestPreviewAuthorizationMapping(t *testing.T) {
	taskID := "88888888-8888-4888-8888-888888888888"
	tests := []struct {
		method, path, operation, resourceName string
	}{
		{http.MethodPost, APIPathPrefix + "/sessions/session-id/previews?namespace=development", "create", "session-id"},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/previews/" + taskID + "?namespace=development", "get", taskID},
		{http.MethodDelete, APIPathPrefix + "/sessions/session-id/previews/" + taskID + "?namespace=development", "delete", taskID},
		{http.MethodGet, APIPathPrefix + "/sessions/session-id/previews/" + taskID + "/stream?namespace=development", "stream", taskID},
	}
	for _, test := range tests {
		mapped := authorizationRequestForHTTP(httptest.NewRequest(test.method, test.path, nil))
		if mapped.Operation != test.operation || mapped.ResourceKind != "previews" ||
			mapped.ResourceName != test.resourceName || mapped.Namespace != "development" {
			t.Fatalf("%s %s authorization request = %#v", test.method, test.path, mapped)
		}
	}
}

func TestPodFileAuthorizationMapping(t *testing.T) {
	taskID := "55555555-5555-4555-8555-555555555555"
	tests := []struct {
		method, suffix, operation, resourceName string
	}{
		{http.MethodPost, "list", "list", "session-id"},
		{http.MethodPost, "create", "create", "session-id"},
		{http.MethodPost, "rename", "update", "session-id"},
		{http.MethodPost, "delete", "delete", "session-id"},
		{http.MethodGet, "operations/" + taskID, "get", taskID},
	}
	for _, test := range tests {
		path := APIPathPrefix + "/sessions/session-id/pod-files/" + test.suffix + "?namespace=development"
		mapped := authorizationRequestForHTTP(httptest.NewRequest(test.method, path, nil))
		if mapped.Operation != test.operation || mapped.ResourceKind != "pod-files" ||
			mapped.ResourceName != test.resourceName || mapped.Namespace != "development" {
			t.Fatalf("%s %s authorization request = %#v", test.method, path, mapped)
		}
	}
}

func authorizedRouterRequest(request *http.Request) *http.Request {
	authorizationRequest := authorizationRequestForHTTP(request)
	return request.WithContext(context.WithValue(request.Context(), authorizationContextKey{}, authorizationContextValue{
		Request: authorizationRequest,
		Decision: authorization.Decision{
			Allowed: true,
			RuleID:  "test-router",
		},
	}))
}
