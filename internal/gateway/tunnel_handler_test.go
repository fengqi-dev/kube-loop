package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
)

func TestSharedTunnelHandlerRoutesByWebSocketSubprotocol(t *testing.T) {
	t.Parallel()

	control := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	forward := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})
	handler := NewTunnelHandler(control, forward)

	tests := []struct {
		name        string
		subprotocol string
		wantStatus  int
	}{
		{name: "control", subprotocol: websocketmux.Subprotocol, wantStatus: http.StatusNoContent},
		{
			name: "control among protocols", subprotocol: "other, " + websocketmux.Subprotocol,
			wantStatus: http.StatusNoContent,
		},
		{name: "Trojan forward", wantStatus: http.StatusAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
			if test.subprotocol != "" {
				request.Header.Set("Sec-WebSocket-Protocol", test.subprotocol)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestTunnelHandlerWithoutForwardUsesControl(t *testing.T) {
	t.Parallel()

	control := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/tunnel", nil)
	response := httptest.NewRecorder()

	NewTunnelHandler(control, nil).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}
