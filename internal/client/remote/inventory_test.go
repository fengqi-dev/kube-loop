package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestPodsNormalizesMissingCollections(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"items":[{"name":"api-0","namespace":"development","ready":true}]}`))
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	pods, err := client.Pods(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL}, "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Containers == nil || pods[0].Ports == nil {
		t.Fatalf("pods = %#v", pods)
	}
}

func TestInventoryPaginationIsBoundedAndStable(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	refresher := &fakeRefresher{now: now}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("limit") != "500" {
			t.Errorf("limit = %q", request.URL.Query().Get("limit"))
		}
		if request.URL.Query().Get("continue") == "" {
			_, _ = writer.Write([]byte(`{"items":[{"name":"alpha","status":"Active"}],"continue":"page-2"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"items":[{"name":"beta","status":"Active"}]}`))
	}))
	defer server.Close()
	client, err := New(store, refresher, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.Namespaces(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "alpha" || items[1].Name != "beta" {
		t.Fatalf("namespaces = %#v", items)
	}
}

func TestGatewayErrorsDoNotLeakRemoteDetailsOrTokens(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write(
			[]byte(
				`{"error":{"code":"FORBIDDEN","message":"operation is not permitted","requestId":"request-1"},"secret":"upstream detail"}`,
			),
		)
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Pods(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL}, "development")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "FORBIDDEN" || apiError.RequestID != "request-1" {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "upstream detail") || strings.Contains(err.Error(), "access-token") {
		t.Fatalf("sensitive response leaked: %v", err)
	}
}
