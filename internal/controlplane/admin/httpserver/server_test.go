package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/labstack/echo/v5"
)

type testRoutes struct{}

func (testRoutes) RegisterRoutes(group *echo.Group) {
	group.GET("/test", func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) })
}

func TestServerExposesOnlyManagementAndLoginRoutes(t *testing.T) {
	management := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ui" {
			t.Fatalf("management path = %q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusOK)
	})
	server, err := New(Config{ListenAddress: "127.0.0.1:0"}, management, testRoutes{},
		controlplane.AuthMethodSourceFunc(func() []controlplane.AuthMethod {
			return []controlplane.AuthMethod{{ID: "company", Type: "oidc", Interaction: "browser"}}
		}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		path   string
		status int
	}{
		{name: "management", path: controlplane.APIPathPrefix + "/admin/ui", status: http.StatusOK},
		{name: "auth", path: "/test", status: http.StatusNoContent},
		{name: "discovery", path: controlplane.DiscoveryPath, status: http.StatusOK},
		{name: "ordinary API excluded", path: controlplane.APIPathPrefix + "/status", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}
