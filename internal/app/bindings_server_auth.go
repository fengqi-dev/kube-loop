package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/google/uuid"
)

type AuthSession struct {
	Authenticated    bool      `json:"authenticated"`
	UserName         string    `json:"userName,omitempty"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
}

func (a *App) LoginServerOIDC(profileID, providerID string) (AuthSession, error) {
	serverProfile, method, err := a.authenticationTarget(profileID, providerID, "oidc")
	if err != nil {
		return AuthSession{}, err
	}
	if method.Interaction != "browser" {
		return AuthSession{}, errors.New("selected provider does not support browser login")
	}
	deviceID, err := a.deviceID(serverProfile.ID)
	if err != nil {
		return AuthSession{}, err
	}
	credential, err := a.auth.LoginOIDC(a.context(), serverProfile.BaseURL, providerID, deviceID)
	if err != nil {
		return AuthSession{}, err
	}
	return a.persistCredential(serverProfile, credential)
}

func (a *App) LoginServerAD(profileID, providerID, username, password string) (AuthSession, error) {
	serverProfile, method, err := a.authenticationTarget(profileID, providerID, "ad")
	if err != nil {
		password = ""
		return AuthSession{}, err
	}
	if method.Interaction != "password" {
		password = ""
		return AuthSession{}, errors.New("selected provider does not support password login")
	}
	deviceID, err := a.deviceID(serverProfile.ID)
	if err != nil {
		password = ""
		return AuthSession{}, err
	}
	passwordBytes := []byte(password)
	password = ""
	credential, err := a.auth.LoginAD(a.context(), serverProfile.BaseURL, providerID, username, passwordBytes, deviceID)
	if err != nil {
		return AuthSession{}, err
	}
	session, err := a.persistCredential(serverProfile, credential)
	if err != nil {
		return AuthSession{}, err
	}
	serverProfile.LastUserName = strings.TrimSpace(username)
	if err := a.profiles.Upsert(serverProfile); err != nil {
		return AuthSession{}, fmt.Errorf("remember authenticated user: %w", err)
	}
	return session, nil
}

func (a *App) LoginServerStaticToken(profileID, providerID, staticToken string) (AuthSession, error) {
	serverProfile, method, err := a.authenticationTarget(profileID, providerID, "static-token")
	if err != nil {
		staticToken = ""
		return AuthSession{}, err
	}
	if method.Interaction != "token" {
		staticToken = ""
		return AuthSession{}, errors.New("selected provider does not support token login")
	}
	deviceID, err := a.deviceID(serverProfile.ID)
	if err != nil {
		staticToken = ""
		return AuthSession{}, err
	}
	presented := []byte(staticToken)
	staticToken = ""
	credential, err := a.auth.LoginStaticToken(a.context(), serverProfile.BaseURL, providerID, presented, deviceID)
	clear(presented)
	if err != nil {
		return AuthSession{}, err
	}
	return a.persistCredential(serverProfile, credential)
}

func (a *App) LoginServerAnonymous(profileID, providerID string) (AuthSession, error) {
	serverProfile, method, err := a.authenticationTarget(profileID, providerID, "anonymous")
	if err != nil {
		return AuthSession{}, err
	}
	if method.Interaction != "none" {
		return AuthSession{}, errors.New("selected provider does not support anonymous login")
	}
	deviceID, err := a.deviceID(serverProfile.ID)
	if err != nil {
		return AuthSession{}, err
	}
	credential, err := a.auth.LoginAnonymous(a.context(), serverProfile.BaseURL, providerID, deviceID)
	if err != nil {
		return AuthSession{}, err
	}
	return a.persistCredential(serverProfile, credential)
}

func (a *App) ServerAuthStatus(profileID string) (AuthSession, error) {
	if a.credentials == nil {
		return AuthSession{}, errors.New("system credential store is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return AuthSession{}, err
	}
	credential, err := a.credentials.Get(serverProfile.ID)
	if errors.Is(err, credentials.ErrNotFound) {
		return AuthSession{}, nil
	}
	if err != nil {
		return AuthSession{}, err
	}
	return authSession(credential), nil
}

func (a *App) RefreshServerLogin(profileID string) (AuthSession, error) {
	if a.auth == nil || a.credentials == nil {
		return AuthSession{}, errors.New("authentication is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return AuthSession{}, err
	}
	current, err := a.credentials.Get(serverProfile.ID)
	if err != nil {
		return AuthSession{}, err
	}
	credential, err := a.auth.Refresh(a.context(), serverProfile.BaseURL, current)
	if err != nil {
		return AuthSession{}, err
	}
	return a.persistCredential(serverProfile, credential)
}

func (a *App) LogoutServer(profileID string) error {
	if a.auth == nil || a.credentials == nil {
		return errors.New("authentication is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	a.stopServerInventoryWatch(serverProfile.ID)
	var disconnectErr error
	if a.remoteFiles != nil {
		disconnectErr = a.remoteFiles.StopProfile(serverProfile.ID)
	}
	if a.remoteExecs != nil {
		disconnectErr = errors.Join(disconnectErr, a.remoteExecs.StopProfile(serverProfile.ID))
	}
	if a.remoteSSH != nil {
		disconnectErr = errors.Join(disconnectErr, a.remoteSSH.StopProfile(serverProfile.ID))
	}
	if a.remoteForwards != nil {
		disconnectErr = errors.Join(disconnectErr, a.remoteForwards.StopProfile(a.context(), serverProfile.ID))
	}
	if a.remoteExchanges != nil {
		disconnectErr = errors.Join(disconnectErr, a.remoteExchanges.StopProfile(a.context(), serverProfile.ID))
	}
	if a.remoteMirrors != nil {
		disconnectErr = errors.Join(disconnectErr, a.remoteMirrors.StopProfile(a.context(), serverProfile.ID))
	}
	if a.remotePreviews != nil {
		disconnectErr = errors.Join(disconnectErr, a.remotePreviews.StopProfile(a.context(), serverProfile.ID))
	}
	if a.dataPlanes != nil {
		disconnectErr = errors.Join(disconnectErr, a.dataPlanes.Disconnect(serverProfile.ID))
	}
	if a.remoteSessions != nil {
		disconnectErr = errors.Join(disconnectErr, a.remoteSessions.Disconnect(a.context(), serverProfile.ID))
	}
	credential, err := a.credentials.Get(serverProfile.ID)
	if errors.Is(err, credentials.ErrNotFound) {
		return disconnectErr
	}
	if err != nil {
		return errors.Join(disconnectErr, err)
	}
	revokeErr := a.auth.Revoke(a.context(), serverProfile.BaseURL, credential.RefreshToken)
	deleteErr := a.credentials.Delete(serverProfile.ID)
	return errors.Join(disconnectErr, revokeErr, deleteErr)
}

func (a *App) authenticationTarget(profileID, providerID, providerType string) (clientprofile.Profile, clientdiscovery.AuthMethod, error) {
	if a.auth == nil || a.credentials == nil {
		return clientprofile.Profile{}, clientdiscovery.AuthMethod{}, errors.New("authentication is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientprofile.Profile{}, clientdiscovery.AuthMethod{}, err
	}
	document, err := a.TestServerAddress(serverProfile.BaseURL)
	if err != nil {
		return clientprofile.Profile{}, clientdiscovery.AuthMethod{}, err
	}
	for _, method := range document.AuthMethods {
		if method.ID == providerID && method.Type == providerType {
			return serverProfile, method, nil
		}
	}
	return clientprofile.Profile{}, clientdiscovery.AuthMethod{}, errors.New("selected authentication provider is not advertised by this server")
}

func (a *App) serverProfile(profileID string) (clientprofile.Profile, error) {
	if a.profiles == nil {
		return clientprofile.Profile{}, errors.New("Server Profile store is unavailable")
	}
	state := a.profiles.Snapshot()
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = state.ActiveProfileID
	}
	for _, serverProfile := range state.Profiles {
		if serverProfile.ID == profileID {
			return serverProfile, nil
		}
	}
	return clientprofile.Profile{}, errors.New("Server Profile not found")
}

func (a *App) deviceID(profileID string) (string, error) {
	credential, err := a.credentials.Get(profileID)
	if err == nil && strings.TrimSpace(credential.DeviceID) != "" {
		return credential.DeviceID, nil
	}
	if err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return "", err
	}
	return uuid.NewString(), nil
}

func (a *App) persistCredential(serverProfile clientprofile.Profile, credential credentials.Credential) (AuthSession, error) {
	if err := a.credentials.Set(serverProfile.ID, credential); err != nil {
		_ = a.auth.Revoke(a.context(), serverProfile.BaseURL, credential.RefreshToken)
		return AuthSession{}, err
	}
	session := authSession(credential)
	if session.UserName != "" && session.UserName != serverProfile.LastUserName {
		serverProfile.LastUserName = session.UserName
		if err := a.profiles.Upsert(serverProfile); err != nil {
			_ = a.credentials.Delete(serverProfile.ID)
			_ = a.auth.Revoke(a.context(), serverProfile.BaseURL, credential.RefreshToken)
			return AuthSession{}, fmt.Errorf("remember authenticated user: %w", err)
		}
	}
	return session, nil
}

func authSession(credential credentials.Credential) AuthSession {
	return AuthSession{
		Authenticated: true, UserName: tokenUserName(credential.AccessToken), AccessExpiresAt: credential.AccessExpiresAt,
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
	for _, value := range []string{claims.PreferredUserName, claims.UserName, claims.Name, claims.Email, claims.Subject} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
