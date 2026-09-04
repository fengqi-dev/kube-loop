package remote

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type memoryStore struct {
	mu    sync.Mutex
	value credentials.Credential
}

func (store *memoryStore) Set(_ string, credential credentials.Credential) error {
	store.mu.Lock()
	store.value = credential
	store.mu.Unlock()
	return nil
}

func (store *memoryStore) Get(string) (credentials.Credential, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.value.AccessToken == "" {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return store.value, nil
}

func (store *memoryStore) Delete(string) error {
	store.mu.Lock()
	store.value = credentials.Credential{}
	store.mu.Unlock()
	return nil
}

type fakeRefresher struct {
	calls atomic.Int32
	now   time.Time
}

type rejectingRefresher struct{}

func (rejectingRefresher) Refresh(
	context.Context,
	string,
	credentials.Credential,
) (credentials.Credential, error) {
	return credentials.Credential{}, &clientauth.APIError{
		Status: http.StatusBadRequest,
		Code:   clientauth.CodeInvalidGrant,
	}
}

func (refresher *fakeRefresher) Refresh(
	_ context.Context,
	_ string,
	current credentials.Credential,
) (credentials.Credential, error) {
	refresher.calls.Add(1)
	current.AccessToken = "access-new"
	current.RefreshToken = "refresh-new"
	current.AccessExpiresAt = refresher.now.Add(time.Minute)
	current.RefreshExpiresAt = refresher.now.Add(time.Hour)
	return current, nil
}

func testNetworkSpec(t *testing.T) (networkspec.Spec, string) {
	t.Helper()
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, hash
}

func validCredential(now time.Time) credentials.Credential {
	return credentials.Credential{
		AccessToken: "access-token", RefreshToken: "refresh-token", AccessExpiresAt: now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour), DeviceID: "device-1",
	}
}
