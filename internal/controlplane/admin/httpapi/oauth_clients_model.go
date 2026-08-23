package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

type oauthClientInput struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Public       bool     `json:"public"`
	RedirectURIs []string `json:"redirectUris"`
	GrantTypes   []string `json:"grantTypes"`
	Scopes       []string `json:"scopes"`
	Trusted      bool     `json:"trusted"`
	Enabled      bool     `json:"enabled"`
	Reason       string   `json:"reason"`
}

func oauthClientFromInput(input oauthClientInput) (storage.OAuthClient, error) {
	now := time.Now().UTC()
	client := storage.OAuthClient{
		ID:           strings.TrimSpace(input.ID),
		Name:         strings.TrimSpace(input.Name),
		Public:       input.Public,
		RedirectURIs: input.RedirectURIs,
		GrantTypes:   input.GrantTypes,
		Scopes:       input.Scopes,
		Trusted:      input.Trusted,
		Enabled:      input.Enabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	allowedGrants := []string{
		"authorization_code",
		"refresh_token",
		"client_credentials",
	}
	for _, grant := range client.GrantTypes {
		if !slices.Contains(allowedGrants, grant) {
			return storage.OAuthClient{}, errors.New(
				"oauth client grant type is invalid",
			)
		}
	}
	if slices.Contains(client.GrantTypes, "refresh_token") &&
		!slices.Contains(client.Scopes, "offline_access") {
		return storage.OAuthClient{}, errors.New(
			"refresh token clients require the offline_access scope",
		)
	}
	if slices.Contains(client.GrantTypes, "client_credentials") {
		for _, scope := range []string{"openid", "profile", emailField, "offline_access"} {
			if slices.Contains(client.Scopes, scope) {
				return storage.OAuthClient{}, errors.New(
					"client credentials clients cannot allow identity scopes",
				)
			}
		}
	}
	if client.Public &&
		slices.Contains(client.GrantTypes, "client_credentials") {
		return storage.OAuthClient{}, errors.New(
			"public OAuth clients cannot use client credentials",
		)
	}
	for _, redirect := range client.RedirectURIs {
		parsed, err := url.Parse(redirect)
		desktopCallback := client.ID == storage.DesktopOAuthClientID &&
			redirect == storage.DesktopOAuthRedirectURI
		loopbackHTTP := parsed.Scheme == "http" &&
			(parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1" || parsed.Hostname() == "localhost")
		allowedScheme := desktopCallback || parsed.Scheme == schemeHTTPS ||
			loopbackHTTP
		if err != nil || parsed.User != nil || parsed.Fragment != "" ||
			parsed.Host == "" ||
			!allowedScheme {
			return storage.OAuthClient{}, errors.New(
				"oauth client redirect URI is invalid",
			)
		}
	}
	return client, nil
}

func oauthClientDocument(client storage.OAuthClient) map[string]any {
	return map[string]any{
		"id": client.ID, nameField: client.Name, publicField: client.Public,
		redirectURIsField: client.RedirectURIs, grantTypesField: client.GrantTypes,
		scopesField: client.Scopes, "trusted": client.Trusted,
		enabledField: client.Enabled, "builtin": client.Builtin,
		"machineIdentityId": client.MachineIdentityID,
		"createdAt":         client.CreatedAt,
		"updatedAt":         client.UpdatedAt,
	}
}

func (api *readAPI) oauthClientError(ctx *echo.Context, err error) error {
	status, code, message := http.StatusBadRequest, invalidRequestCode, "OAuth client request is invalid"
	if errors.Is(err, storage.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "OAuth client was not found"
	} else if errors.Is(err, storage.ErrConflict) {
		status, code, message = http.StatusConflict, "conflict", "OAuth client already exists"
	}
	return writeError(ctx, status, code, message, requestID(ctx.Request()))
}
