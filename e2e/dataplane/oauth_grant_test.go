//go:build e2e

package dataplane

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func createOAuthGrant(t *testing.T, ctx context.Context, store *storage.Store,
	authorizationID, principalID, deviceID string, signatureByte byte, createdAt, expiresAt time.Time,
) {
	t.Helper()
	if err := store.OAuthSessions().Create(ctx, storage.OAuthSession{
		Kind: "access_token", SignatureHash: bytes.Repeat([]byte{signatureByte}, 32), RequestID: authorizationID,
		PrincipalID: principalID, ClientID: "e2e-client", DeviceID: deviceID,
		RequestJSON: json.RawMessage(`{}`), CreatedAt: createdAt, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
}
