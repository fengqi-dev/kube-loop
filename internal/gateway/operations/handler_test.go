package operations

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeGatewayState struct {
	draining bool
	unready  bool
	active   int
}

func (state fakeGatewayState) Ready() bool            { return !state.unready }
func (state fakeGatewayState) Draining() bool         { return state.draining }
func (state fakeGatewayState) ActiveConnections() int { return state.active }

type fakeWebSocketState struct {
	draining bool
	active   int
}

func (state fakeWebSocketState) Draining() bool      { return state.draining }
func (state fakeWebSocketState) ActiveSessions() int { return state.active }

func TestHealthReflectsDrainState(t *testing.T) {
	ready := NewHandler(fakeGatewayState{}, fakeWebSocketState{})
	response := httptest.NewRecorder()
	ready.ServeHTTP(response, httptest.NewRequest(http.MethodGet, ReadyPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d", response.Code)
	}

	draining := NewHandler(fakeGatewayState{draining: true}, fakeWebSocketState{})
	response = httptest.NewRecorder()
	draining.ServeHTTP(response, httptest.NewRequest(http.MethodGet, ReadyPath, nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "draining") {
		t.Fatalf("draining response = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	draining.ServeHTTP(response, httptest.NewRequest(http.MethodGet, LivePath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("live status while draining = %d", response.Code)
	}
}

func TestReadinessDistinguishesUnavailableDependencyFromDrain(t *testing.T) {
	handler := NewHandler(fakeGatewayState{unready: true}, fakeWebSocketState{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, ReadyPath, nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "unavailable") {
		t.Fatalf("unavailable response = %d %q", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, LivePath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("live status while dependency unavailable = %d", response.Code)
	}
}

func TestMetricsExposeOnlyAggregateRuntimeState(t *testing.T) {
	handler := NewHandler(fakeGatewayState{active: 7}, fakeWebSocketState{active: 3})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, MetricsPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, metric := range []string{
		"kubeloop_gateway_ready 1",
		"kubeloop_gateway_draining 0",
		"kubeloop_gateway_active_connections 7",
		"kubeloop_gateway_active_websocket_sessions 3",
	} {
		if !strings.Contains(response.Body.String(), metric) {
			t.Errorf("metrics missing %q: %s", metric, response.Body.String())
		}
	}
	for _, sensitive := range []string{"token", "email", "identity", "session_id", "target", "endpoint"} {
		if strings.Contains(strings.ToLower(response.Body.String()), sensitive) {
			t.Errorf("metrics expose sensitive or high-cardinality field %q: %s", sensitive, response.Body.String())
		}
	}

	unavailable := NewHandler(fakeGatewayState{unready: true}, fakeWebSocketState{})
	response = httptest.NewRecorder()
	unavailable.ServeHTTP(response, httptest.NewRequest(http.MethodGet, MetricsPath, nil))
	if !strings.Contains(response.Body.String(), "kubeloop_gateway_ready 0") {
		t.Fatalf("unavailable metrics = %s", response.Body.String())
	}
}

func TestOperationalEndpointsOnlyAllowGET(t *testing.T) {
	handler := NewHandler(fakeGatewayState{}, fakeWebSocketState{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, LivePath, nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status = %d, Allow = %q", response.Code, response.Header().Get("Allow"))
	}
}
