package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
)

func (client *Client) LoginOIDC(
	ctx context.Context,
	baseURL, providerID, deviceID string,
) (credentials.Credential, error) {
	baseURL, err := validateTarget(baseURL, providerID)
	if err != nil {
		return credentials.Credential{}, err
	}
	if strings.TrimSpace(deviceID) == "" {
		return credentials.Credential{}, errors.New("device ID is required")
	}
	if client.openBrowser == nil {
		return credentials.Credential{}, errors.New("browser integration is unavailable")
	}
	state, err := randomValue(32)
	if err != nil {
		return credentials.Credential{}, err
	}
	nonce, err := randomValue(32)
	if err != nil {
		return credentials.Credential{}, err
	}
	verifier, err := randomValue(32)
	if err != nil {
		return credentials.Credential{}, err
	}
	verifierHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(verifierHash[:])
	metadata, err := client.discoverProvider(ctx, baseURL)
	if err != nil {
		return credentials.Credential{}, err
	}
	authorizationURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return credentials.Credential{}, errors.New("create authorization URL")
	}
	loginDeadline := time.Now().Add(client.loginTimeout)
	loginContext, cancel := context.WithDeadline(ctx, loginDeadline)
	defer cancel()
	callback, err := client.beginCallback(state)
	if err != nil {
		return credentials.Credential{}, err
	}
	defer client.endCallback(callback)
	redirectURI := client.redirectURI
	if client.loopbackCallback {
		actualRedirectURI, closeCallback, callbackErr := client.startLoopbackCallback(loginContext)
		if callbackErr != nil {
			return credentials.Credential{}, callbackErr
		}
		redirectURI = actualRedirectURI
		defer closeCallback()
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set(authParamClientID, client.clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", "openid profile email offline_access kubeloop.api")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("provider", providerID)
	authorizationURL.RawQuery = query.Encode()
	if err := client.openBrowser(authorizationURL.String()); err != nil {
		return credentials.Credential{}, errors.New("open system browser")
	}
	var result callbackResult
	select {
	case result = <-callback.result:
		if client.browserCallback != nil {
			client.browserCallback()
		}
	case <-loginContext.Done():
		if errors.Is(loginContext.Err(), context.DeadlineExceeded) {
			return credentials.Credential{}, errors.New("browser login timed out")
		}
		return credentials.Credential{}, errors.New("browser login was cancelled")
	}
	if result.err != nil {
		return credentials.Credential{}, result.err
	}
	var response tokenResponse
	if err := client.postForm(loginContext, metadata.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {result.code}, "code_verifier": {verifier},
		authParamClientID: {client.clientID}, "redirect_uri": {redirectURI}, "device_id": {deviceID},
	}, &response); err != nil {
		return credentials.Credential{}, err
	}
	return credentialFromResponse(response, deviceID)
}
