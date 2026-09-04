package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestConcurrentExpiredRequestsRotateRefreshTokenOnce(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: credentials.Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: now.Add(-time.Second),
		RefreshExpiresAt: now.Add(time.Hour), DeviceID: "device-1",
	}}
	refresher := &fakeRefresher{now: now}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-new" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	client, err := New(store, refresher, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, requestErr := client.Namespaces(context.Background(), serverProfile)
			errorsChannel <- requestErr
		}()
	}
	close(start)
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if refresher.calls.Load() != 1 {
		t.Fatalf("refresh calls = %d", refresher.calls.Load())
	}
}

func TestInvalidRefreshGrantClearsCredential(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: credentials.Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: now.Add(-time.Second),
		DeviceID: "device-1",
	}}
	client, err := New(store, rejectingRefresher{}, Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.usableCredential(
		context.Background(),
		profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"},
		"",
	)
	if !errors.Is(err, clientauth.ErrLoginExpired) {
		t.Fatalf("refresh error = %v, want ErrLoginExpired", err)
	}
	if _, err := store.Get("service-1"); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("credential remains after invalid grant: %v", err)
	}
}

func TestKnownRefreshExpiryClearsCredentialWithoutNetworkRefresh(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: credentials.Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: now.Add(-time.Second),
		RefreshExpiresAt: now.Add(-time.Minute), DeviceID: "device-1",
	}}
	refresher := &fakeRefresher{now: now}
	client, err := New(store, refresher, Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.usableCredential(
		context.Background(),
		profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"},
		"",
	)
	if !errors.Is(err, clientauth.ErrLoginExpired) || refresher.calls.Load() != 0 {
		t.Fatalf("expired refresh result: err=%v refresh calls=%d", err, refresher.calls.Load())
	}
	if _, err := store.Get("service-1"); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("credential remains after known expiry: %v", err)
	}
}

func TestUnauthorizedResponseRefreshesAndRetriesOnce(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: credentials.Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour), DeviceID: "device-1",
	}}
	refresher := &fakeRefresher{now: now}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Authorization") == "Bearer access-old" {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":{"code":"UNAUTHENTICATED","message":"expired","requestId":"one"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.31.0","gatewayVersion":"v2-test"}`))
	}))
	defer server.Close()
	client, err := New(store, refresher, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Version(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if result.GitVersion != "v1.31.0" || calls.Load() != 2 || refresher.calls.Load() != 1 {
		t.Fatalf("result = %#v, HTTP calls = %d, refresh calls = %d", result, calls.Load(), refresher.calls.Load())
	}
}
