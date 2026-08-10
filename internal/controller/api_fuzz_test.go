package controller

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
)

func FuzzGatewayHTTPEntryBoundedAndRedacted(f *testing.F) {
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "fuzz", Subjects: []string{"*"}, Namespaces: []string{"*"}, Operations: []string{"*"}, ResourceKinds: []string{"*"},
	}}})
	if err != nil {
		f.Fatal(err)
	}
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test", MaxRequestBodyBytes: 256}, BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthenticator(AuthenticatorFunc(func(*http.Request) (Principal, *APIError) {
			return Principal{Subject: "fuzz-principal"}, nil
		})),
		WithAuthorizer(policy),
		WithAPIHandler(APIHandlerFunc(func(writer http.ResponseWriter, request *http.Request, _ Principal) *APIError {
			if request.Method == http.MethodPost {
				var body struct {
					Value string `json:"value"`
				}
				if apiError := DecodeJSON(request, &body); apiError != nil {
					return apiError
				}
			}
			writer.WriteHeader(http.StatusNoContent)
			return nil
		})),
	)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(true, "resource", `{"value":"ok"}`, "application/json")
	f.Add(false, "../secret", "", "text/plain")
	f.Fuzz(func(t *testing.T, post bool, path, body, contentType string) {
		if len(path) > 512 || len(body) > 2048 || len(contentType) > 256 {
			t.Skip()
		}
		const secret = "gateway-fuzz-secret-marker"
		method := http.MethodGet
		if post {
			method = http.MethodPost
		}
		request := httptest.NewRequest(method, APIPathPrefix+"/fuzz/"+url.PathEscape(path), strings.NewReader(body+secret))
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Body.Len() > 16<<10 {
			t.Fatalf("response exceeded bound: %d", response.Body.Len())
		}
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("request body or bearer token leaked into response")
		}
	})
}
