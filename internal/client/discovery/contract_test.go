package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContractClientAndGatewayVersionMatrix(t *testing.T) {
	tests := []struct {
		name         string
		client       string
		gatewayMin   string
		gatewayMax   string
		wantMismatch bool
	}{
		{name: "old client new Gateway overlap", client: "2.0", gatewayMin: "2.0", gatewayMax: "2.1"},
		{name: "new client old Gateway no overlap", client: "2.1", gatewayMin: "2.0", gatewayMax: "2.0", wantMismatch: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(writer).Encode(Document{
					ServiceID: "service", PublicURL: server.URL, TunnelPath: "/tunnel",
					APIVersions: []string{"v2"}, AuthMethods: []AuthMethod{}, Features: []string{},
					ServerVersion: "2.0.0", ProtocolMin: test.gatewayMin, ProtocolMax: test.gatewayMax,
				})
			}))
			defer server.Close()
			_, err := New(Config{HTTPClient: server.Client(), ProtocolVersion: test.client}).Discover(context.Background(), server.URL)
			if !test.wantMismatch {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var compatibilityError *CompatibilityError
			if !errors.As(err, &compatibilityError) || compatibilityError.Code != CodeVersionMismatch {
				t.Fatalf("version contract = %#v, %v", compatibilityError, err)
			}
		})
	}
}
