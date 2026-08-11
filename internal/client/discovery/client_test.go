package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscoverValidatesAndReturnsDocument(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != Path || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("request = %s, Accept = %q", request.URL.Path, request.Header.Get("Accept"))
		}
		_ = json.NewEncoder(writer).Encode(Document{
			ServiceID: "production", PublicURL: server.URL + "/",
			TunnelPath:  "/tunnel",
			APIVersions: []string{"v1", "v2"},
			AuthMethods: []AuthMethod{
				{ID: "company", Type: "oidc", Interaction: "browser"},
				{ID: "ad", Type: "ad", Interaction: "password"},
				{ID: "local", Type: "static-token", Interaction: "token"},
				{ID: "guest", Type: "anonymous", Interaction: "none"},
			},
			Features: []string{"sessions"}, ServerVersion: "2.1.0", ProtocolMin: "2.0", ProtocolMax: "2.1", MinClientVersion: "2.0.0",
		})
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client(), ClientVersion: "v2.0.1"})
	document, err := client.Discover(context.Background(), server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if document.ServiceID != "production" || document.PublicURL != server.URL || document.TunnelPath != "/tunnel" || len(document.AuthMethods) != 4 {
		t.Fatalf("document = %#v", document)
	}
}

func TestDiscoverPreservesExplicitConfiguredScheme(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(Document{
			ServiceID: "development", PublicURL: strings.Replace(server.URL, "http://", "https://", 1),
			TunnelPath: "/tunnel", APIVersions: []string{"v2"},
			AuthMethods: []AuthMethod{{ID: "guest", Type: "anonymous", Interaction: "none"}},
			ProtocolMin: "2.0", ProtocolMax: "2.0",
		})
	}))
	defer server.Close()

	document, err := New(Config{}).Discover(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if document.PublicURL != server.URL {
		t.Fatalf("public URL = %q, want configured %q", document.PublicURL, server.URL)
	}
}

func TestDiscoverRejectsRedirectsAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{name: "redirect", want: "redirects", handler: func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "https://other.example.test/.well-known/kubeloop", http.StatusFound)
		}},
		{name: "oversized", want: "64 KiB", handler: func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(strings.Repeat("x", MaxDocumentBytes+1)))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(test.handler)
			defer server.Close()
			_, err := New(Config{HTTPClient: server.Client()}).Discover(context.Background(), server.URL)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverRejectsMalformedOrIncompatibleDocuments(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Document)
		want   string
	}{
		{name: "service ID", mutate: func(document *Document) { document.ServiceID = "" }, want: "service ID"},
		{name: "unsafe service ID", mutate: func(document *Document) { document.ServiceID = "../service" }, want: "service ID"},
		{name: "origin", mutate: func(document *Document) { document.PublicURL = "https://other.example.test" }, want: "origin"},
		{name: "tunnel URL", mutate: func(document *Document) { document.TunnelPath = "https://other.example.test/tunnel" }, want: "tunnel path"},
		{name: "tunnel traversal", mutate: func(document *Document) { document.TunnelPath = "/relay/../tunnel" }, want: "tunnel path"},
		{name: "API", mutate: func(document *Document) { document.APIVersions = []string{"v1"} }, want: "API v2"},
		{name: "protocol", mutate: func(document *Document) { document.ProtocolMin = "3.0"; document.ProtocolMax = "3.1" }, want: "incompatible"},
		{name: "minimum client", mutate: func(document *Document) { document.MinClientVersion = "3.0.0" }, want: "requires client"},
		{name: "auth", mutate: func(document *Document) { document.AuthMethods[0].Interaction = "password" }, want: "unsupported authentication"},
		{name: "unsafe auth ID", mutate: func(document *Document) { document.AuthMethods[0].ID = "../oidc" }, want: "method ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				document := Document{
					ServiceID: "service", PublicURL: server.URL, APIVersions: []string{"v2"},
					TunnelPath:  "/tunnel",
					AuthMethods: []AuthMethod{{ID: "oidc", Type: "oidc", Interaction: "browser"}},
					ProtocolMin: "2.0", ProtocolMax: "2.0", MinClientVersion: "2.0.0",
				}
				test.mutate(&document)
				_ = json.NewEncoder(writer).Encode(document)
			}))
			defer server.Close()
			_, err := New(Config{HTTPClient: server.Client(), ClientVersion: "2.0.0"}).Discover(context.Background(), server.URL)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverAllowsUnknownFieldsButRejectsTrailingJSON(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"serviceId":"service","publicUrl":"` + server.URL + `","tunnelPath":"/tunnel","apiVersions":["v2"],"authMethods":[],"features":[],"serverVersion":"dev","protocolMin":"2.0","protocolMax":"2.0","future":true}`))
	}))
	defer server.Close()
	if _, err := New(Config{HTTPClient: server.Client()}).Discover(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}

	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{} {}`))
	})
	if _, err := New(Config{HTTPClient: server.Client()}).Discover(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "one JSON") {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestCompatibilityFailuresReturnStableTypedErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "protocol", err: validateProtocol("2.0", "3.0", "3.1"), code: CodeVersionMismatch},
		{name: "client", err: validateClientVersion("2.0.0", "2.1.0"), code: CodeClientVersionUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var compatibilityError *CompatibilityError
			if !errors.As(test.err, &compatibilityError) || compatibilityError.Code != test.code {
				t.Fatalf("compatibility error = %#v, %v", compatibilityError, test.err)
			}
		})
	}
}

func TestDiscoverHonorsTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	_, err := New(Config{HTTPClient: server.Client(), Timeout: 20 * time.Millisecond}).Discover(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
}
