package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeGatewayState struct {
	draining bool
	unready  bool
}

func (state fakeGatewayState) Ready() bool    { return !state.unready }
func (state fakeGatewayState) Draining() bool { return state.draining }

type fakeWebSocketState struct{ draining bool }

func (state fakeWebSocketState) Draining() bool { return state.draining }

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

func TestOperationalEndpointsOnlyAllowGET(t *testing.T) {
	handler := NewHandler(fakeGatewayState{}, fakeWebSocketState{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, LivePath, nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status = %d, Allow = %q", response.Code, response.Header().Get("Allow"))
	}
}
