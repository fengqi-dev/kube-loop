package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) Refresh(
	ctx context.Context,
	baseURL string,
	current credentials.Credential,
) (credentials.Credential, error) {
	baseURL, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return credentials.Credential{}, err
	}
	var response tokenResponse
	metadata, err := client.discoverProvider(ctx, baseURL)
	if err != nil {
		return credentials.Credential{}, err
	}
	if err := client.postForm(ctx, metadata.TokenEndpoint, url.Values{
		"grant_type": {authParamRefreshToken}, authParamRefreshToken: {current.RefreshToken},
		authParamClientID: {client.clientID}, "device_id": {current.DeviceID},
	}, &response); err != nil {
		return credentials.Credential{}, err
	}
	next, err := credentialFromResponse(response, current.DeviceID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if next.IdentityID == "" {
		next.IdentityID = current.IdentityID
	}
	if next.UserName == "" {
		next.UserName = current.UserName
	}
	return next, nil
}

func (client *Client) Revoke(ctx context.Context, baseURL, refreshToken string) error {
	baseURL, err := profile.NormalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	metadata, err := client.discoverProvider(ctx, baseURL)
	if err != nil {
		return err
	}
	return client.postForm(ctx, metadata.RevocationEndpoint, url.Values{
		"token": {refreshToken}, "token_type_hint": {authParamRefreshToken}, authParamClientID: {client.clientID},
	}, nil)
}

func credentialFromResponse(response tokenResponse, deviceID string) (credentials.Credential, error) {
	missingToken := response.AccessToken == "" || response.RefreshToken == ""
	if !strings.EqualFold(response.TokenType, authorizationTypeBearer) || missingToken || response.ExpiresIn <= 0 {
		return credentials.Credential{}, errors.New("oAuth server returned an incomplete token response")
	}
	identityID, userName := identityFromIDToken(response.IDToken)
	return credentials.Credential{
		TokenType:       authorizationTypeBearer,
		AccessToken:     response.AccessToken,
		AccessExpiresAt: time.Now().Add(time.Duration(response.ExpiresIn) * time.Second),
		RefreshToken:    response.RefreshToken,
		DeviceID:        deviceID,
		IdentityID:      identityID,
		UserName:        userName,
	}, nil
}

func identityFromIDToken(token string) (string, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Subject           string `json:"sub"`
		Name              string `json:"name"`
		PreferredUserName string `json:"preferred_username"`
		UserName          string `json:"username"`
		Email             string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", ""
	}
	identityID := strings.TrimSpace(claims.Subject)
	for _, value := range []string{claims.Name, claims.PreferredUserName, claims.UserName, claims.Email, identityID} {
		if value = strings.TrimSpace(value); value != "" {
			return identityID, value
		}
	}
	return identityID, ""
}
