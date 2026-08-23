package app

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
)

type AuthSession struct {
	Authenticated    bool      `json:"authenticated"`
	UserName         string    `json:"userName,omitempty"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"    ts_type:"string"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"   ts_type:"string"`
}

func authSession(credential credentials.Credential) AuthSession {
	userName := strings.TrimSpace(credential.UserName)
	if userName == "" {
		userName = tokenUserName(credential.AccessToken)
	}
	return AuthSession{
		Authenticated: true, UserName: userName, AccessExpiresAt: credential.AccessExpiresAt,
		RefreshExpiresAt: credential.RefreshExpiresAt,
	}
}

func tokenUserName(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		PreferredUserName string `json:"preferred_username"`
		UserName          string `json:"username"`
		Name              string `json:"name"`
		Email             string `json:"email"`
		Subject           string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	for _, value := range []string{claims.Name, claims.PreferredUserName, claims.UserName, claims.Email, claims.Subject} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
