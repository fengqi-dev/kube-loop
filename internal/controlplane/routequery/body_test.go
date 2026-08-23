package routequery

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestRequireEmptyBody(t *testing.T) {
	tests := []struct {
		name    string
		body    *http.Request
		message string
	}{
		{name: "empty", body: httptest.NewRequest(http.MethodGet, "/", nil)},
		{
			name: "non-empty", body: httptest.NewRequest(http.MethodGet, "/", strings.NewReader("x")),
			message: "request body must be empty",
		},
		{
			name: "read failure", body: httptest.NewRequest(http.MethodGet, "/", failingReader{}),
			message: "request body is invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiError := RequireEmptyBody(test.body)
			if test.message == "" {
				if apiError != nil {
					t.Fatalf("RequireEmptyBody() error = %#v", apiError)
				}
				return
			}
			if apiError == nil || apiError.Message != test.message {
				t.Fatalf("RequireEmptyBody() error = %#v", apiError)
			}
		})
	}
}
