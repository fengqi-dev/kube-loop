package app

import (
	"encoding/base64"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
)

func TestTokenUserNamePrefersDisplayName(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"sub":"subject-1","email":"user@example.test","name":"Example User","preferred_username":"fengqi"}`),
	)
	if got := tokenUserName("header." + payload + ".signature"); got != "Example User" {
		t.Fatalf("token username = %q", got)
	}
	fallbackPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"subject-1","preferred_username":"fengqi"}`))
	if got := tokenUserName("header." + fallbackPayload + ".signature"); got != "fengqi" {
		t.Fatalf("fallback token username = %q", got)
	}
	if got := tokenUserName("opaque-token"); got != "" {
		t.Fatalf("opaque token username = %q", got)
	}
}

func TestAuthSessionUsesPersistedOIDCIdentityForOpaqueAccessToken(t *testing.T) {
	session := authSession(credentials.Credential{AccessToken: "opaque-token", UserName: "Example User"})
	if !session.Authenticated || session.UserName != "Example User" {
		t.Fatalf("auth session = %#v", session)
	}
}
