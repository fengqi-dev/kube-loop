package sessionroute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
)

type testSessions struct {
	namespace string
	sessionID string
}

func (sessions testSessions) RequireActive(
	_ context.Context,
	_ controlplaneapi.Identity,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if namespace != sessions.namespace || sessionID != sessions.sessionID {
		return sessionapi.ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound}
	}
	return sessionapi.ActiveSession{ID: sessionID, Namespace: namespace}, nil
}

func TestWithTaskResolvesExactSessionAndTask(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?namespace=development", nil)
	request.SetPathValue("sessionID", "session-1")
	request.SetPathValue("taskID", "task-1")
	ctx := echo.New().NewContext(request, httptest.NewRecorder())
	called := false
	handler := WithTask(
		testSessions{namespace: "development", sessionID: "session-1"},
		func(
			_ *echo.Context,
			_ controlplaneapi.Identity,
			session sessionapi.ActiveSession,
			taskID string,
		) *controlplaneapi.Error {
			called = session.ID == "session-1" && taskID == "task-1"
			return nil
		},
	)
	if apiError := handler(ctx, controlplaneapi.Identity{Subject: "identity-1"}); apiError != nil {
		t.Fatal(apiError)
	}
	if !called {
		t.Fatal("session route did not preserve the resolved Session and Task ID")
	}
}

func TestNamespaceFromQueryRejectsAmbiguousInput(t *testing.T) {
	for _, target := range []string{
		"/?namespace=development&extra=true",
		"/?namespace=development&namespace=production",
		"/?namespace=not/a/namespace",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if _, apiError := NamespaceFromQuery(request); apiError == nil ||
			apiError.Code != controlplaneapi.CodeInvalidArgument || apiError.Field != "namespace" {
			t.Fatalf("NamespaceFromQuery(%q) error = %#v", target, apiError)
		}
	}
}
