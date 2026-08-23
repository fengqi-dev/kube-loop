package routequery

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func TestNamespace(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		want    string
		field   string
		message string
	}{
		{name: "valid", target: "/?namespace=development", want: "development"},
		{
			name: "unknown query", target: "/?namespace=development&watch=true",
			field: "watch", message: "query parameter is not supported",
		},
		{
			name: "duplicate", target: "/?namespace=development&namespace=production",
			field: "namespace", message: "query parameter must be provided once",
		},
		{name: "invalid", target: "/?namespace=not/a/label", field: "namespace", message: "namespace is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			got, apiError := Namespace(request)
			if test.message == "" {
				if apiError != nil || got != test.want {
					t.Fatalf("Namespace() = %q, %#v", got, apiError)
				}
				return
			}
			if apiError == nil || apiError.Code != controlplaneapi.CodeInvalidArgument ||
				apiError.Field != test.field || apiError.Message != test.message {
				t.Fatalf("Namespace() error = %#v", apiError)
			}
		})
	}
}
