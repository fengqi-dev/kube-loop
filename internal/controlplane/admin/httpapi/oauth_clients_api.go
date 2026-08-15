package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"
)

type oauthClientInput struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organizationId,omitempty"`
	Name           string   `json:"name"`
	Public         bool     `json:"public"`
	RedirectURIs   []string `json:"redirectUris"`
	GrantTypes     []string `json:"grantTypes"`
	Scopes         []string `json:"scopes"`
	Trusted        bool     `json:"trusted"`
	Enabled        bool     `json:"enabled"`
	Reason         string   `json:"reason"`
}

func (api *readAPI) listOAuthClients(ctx *echo.Context) error {
	items, err := api.oauthRepositories.OAuthClients().List(ctx.Request().Context())
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	documents := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if !api.authorizeOAuthClient(ctx, item, false) {
			continue
		}
		documents = append(documents, oauthClientDocument(item))
	}
	return ctx.JSON(http.StatusOK, map[string]any{"items": documents})
}

func (api *readAPI) createOAuthClient(ctx *echo.Context) error {
	var input oauthClientInput
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	client, err := oauthClientFromInput(input)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !api.authorizeOAuthClient(ctx, client, true) {
		return nil
	}
	var plaintext string
	err = api.oauthTransactions.WithinTransaction(ctx.Request().Context(), func(repositories storage.Repositories) error {
		if slices.Contains(client.GrantTypes, "client_credentials") {
			identity, identityErr := repositories.Identities().Create(ctx.Request().Context(), storage.Identity{ID: uuid.NewString(), Type: "machine", DisplayName: client.Name, Status: "active", CreatedAt: client.CreatedAt, UpdatedAt: client.UpdatedAt})
			if identityErr != nil {
				return identityErr
			}
			client.MachineIdentityID = identity.ID
		}
		if err := repositories.OAuthClients().Create(ctx.Request().Context(), client); err != nil {
			return err
		}
		if !client.Public {
			var secretErr error
			plaintext, secretErr = generateOAuthClientSecret()
			if secretErr != nil {
				return secretErr
			}
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
			if hashErr != nil {
				return errors.New("hash OAuth client secret")
			}
			return repositories.OAuthClients().SetSecret(ctx.Request().Context(), storage.OAuthClientSecret{ClientID: client.ID, SecretHash: hash, CreatedAt: client.CreatedAt, UpdatedAt: client.UpdatedAt})
		}
		return nil
	})
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.oauth-client.create", "success")
	document := oauthClientDocument(client)
	if plaintext != "" {
		document["clientSecret"] = plaintext
	}
	return ctx.JSON(http.StatusCreated, document)
}

func (api *readAPI) updateOAuthClient(ctx *echo.Context) error {
	var input oauthClientInput
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	input.ID = ctx.Param("clientID")
	existingForAuthorization, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), input.ID)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !api.authorizeOAuthClient(ctx, existingForAuthorization, true) ||
		input.OrganizationID != existingForAuthorization.OrganizationID || !requireIAMETag(ctx, existingForAuthorization.UpdatedAt) {
		return nil
	}
	client, err := oauthClientFromInput(input)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	err = api.oauthTransactions.WithinTransaction(ctx.Request().Context(), func(repositories storage.Repositories) error {
		existing, getErr := repositories.OAuthClients().Get(ctx.Request().Context(), client.ID)
		if getErr != nil {
			return getErr
		}
		client.CreatedAt = existing.CreatedAt
		client.MachineIdentityID = existing.MachineIdentityID
		if slices.Contains(client.GrantTypes, "client_credentials") && client.MachineIdentityID == "" {
			identity, identityErr := repositories.Identities().Create(ctx.Request().Context(), storage.Identity{ID: uuid.NewString(), Type: "machine", DisplayName: client.Name, Status: "active", CreatedAt: client.UpdatedAt, UpdatedAt: client.UpdatedAt})
			if identityErr != nil {
				return identityErr
			}
			client.MachineIdentityID = identity.ID
		}
		return repositories.OAuthClients().Update(ctx.Request().Context(), client)
	})
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	stored, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), client.ID)
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.oauth-client.update", "success")
	ctx.Response().Header().Set("ETag", iamETag(stored.UpdatedAt))
	return ctx.JSON(http.StatusOK, oauthClientDocument(stored))
}

func (api *readAPI) rotateOAuthClientSecret(ctx *echo.Context) error {
	client, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !api.authorizeOAuthClient(ctx, client, true) {
		return nil
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	if client.Public {
		return api.oauthClientError(ctx, errors.New("public OAuth clients do not have secrets"))
	}
	plaintext, err := generateOAuthClientSecret()
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return api.oauthClientError(ctx, errors.New("hash OAuth client secret"))
	}
	now := time.Now().UTC()
	err = api.oauthRepositories.OAuthClients().SetSecret(ctx.Request().Context(), storage.OAuthClientSecret{ClientID: client.ID, SecretHash: hash, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.oauth-client.secret.rotate", "success")
	return ctx.JSON(http.StatusOK, map[string]any{"clientId": client.ID, "clientSecret": plaintext})
}

func (api *readAPI) setOAuthClientEnabled(ctx *echo.Context) error {
	var input struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if !decodePolicyJSON(ctx.Response(), ctx.Request(), &input) || !validChangeReason(input.Reason) {
		return api.invalidIAMMutation(ctx)
	}
	client, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !api.authorizeOAuthClient(ctx, client, true) || !requireIAMETag(ctx, client.UpdatedAt) {
		return nil
	}
	client.Enabled = input.Enabled
	client.UpdatedAt = time.Now().UTC()
	if err := api.oauthRepositories.OAuthClients().Update(ctx.Request().Context(), client); err != nil {
		return api.oauthClientError(ctx, err)
	}
	stored, _ := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), client.ID)
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.oauth-client.enabled.update", "success")
	ctx.Response().Header().Set("ETag", iamETag(stored.UpdatedAt))
	return ctx.JSON(http.StatusOK, oauthClientDocument(stored))
}

func (api *readAPI) deleteOAuthClient(ctx *echo.Context) error {
	client, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !api.authorizeOAuthClient(ctx, client, true) || !validChangeReason(ctx.Request().Header.Get("X-KubeLoop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.oauthRepositories.OAuthClients().Delete(ctx.Request().Context(), client.ID); err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.oauth-client.delete", "success")
	return ctx.NoContent(http.StatusNoContent)
}
func (api *readAPI) revokeOAuthConsent(ctx *echo.Context) error {
	client, err := api.oauthRepositories.OAuthClients().Get(ctx.Request().Context(), ctx.Param("clientID"))
	if err != nil {
		return api.oauthClientError(ctx, err)
	}
	if !api.authorizeOAuthClient(ctx, client, true) || !validChangeReason(ctx.Request().Header.Get("X-KubeLoop-Reason")) {
		return api.invalidIAMMutation(ctx)
	}
	if err := api.oauthRepositories.OAuthConsents().RevokeClient(ctx.Request().Context(), ctx.Param("identityID"), ctx.Param("clientID")); err != nil {
		return api.oauthClientError(ctx, err)
	}
	api.audit(ctx.Request(), subjectFromRequest(ctx.Request()), "iam.oauth-consent.revoke", "success")
	return ctx.NoContent(http.StatusNoContent)
}

func (api *readAPI) authorizeOAuthClient(ctx *echo.Context, client storage.OAuthClient, write bool) bool {
	platformCapability := adminauthorization.Capability("platform.oauth-clients.read")
	organizationCapability := adminauthorization.Capability("org.oauth-clients.read")
	if write {
		platformCapability = "platform.oauth-clients.manage"
		organizationCapability = "org.oauth-clients.manage"
	}
	subject := subjectFromRequest(ctx.Request())
	if api.authorizer.Authorize(ctx.Request().Context(), subject, adminauthorization.Request{Capability: platformCapability}).Allowed {
		return true
	}
	allowed := client.OrganizationID != "" && api.authorizer.Authorize(ctx.Request().Context(), subject,
		adminauthorization.Request{Capability: organizationCapability, OrganizationID: client.OrganizationID}).Allowed
	if !allowed && write {
		writeError(ctx.Response(), http.StatusForbidden, "forbidden", "operation is not permitted", requestID(ctx.Request()))
	}
	return allowed
}

func oauthClientFromInput(input oauthClientInput) (storage.OAuthClient, error) {
	now := time.Now().UTC()
	client := storage.OAuthClient{ID: strings.TrimSpace(input.ID), OrganizationID: strings.TrimSpace(input.OrganizationID), Name: strings.TrimSpace(input.Name), Public: input.Public, RedirectURIs: input.RedirectURIs, GrantTypes: input.GrantTypes, Scopes: input.Scopes, Trusted: input.Trusted, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now}
	allowedGrants := []string{"authorization_code", "refresh_token", "client_credentials"}
	for _, grant := range client.GrantTypes {
		if !slices.Contains(allowedGrants, grant) {
			return storage.OAuthClient{}, errors.New("OAuth client grant type is invalid")
		}
	}
	if slices.Contains(client.GrantTypes, "refresh_token") && !slices.Contains(client.Scopes, "offline_access") {
		return storage.OAuthClient{}, errors.New("refresh token clients require the offline_access scope")
	}
	if slices.Contains(client.GrantTypes, "client_credentials") {
		for _, scope := range []string{"openid", "profile", "email", "offline_access"} {
			if slices.Contains(client.Scopes, scope) {
				return storage.OAuthClient{}, errors.New("client credentials clients cannot allow identity scopes")
			}
		}
	}
	if client.Public && slices.Contains(client.GrantTypes, "client_credentials") {
		return storage.OAuthClient{}, errors.New("public OAuth clients cannot use client credentials")
	}
	for _, redirect := range client.RedirectURIs {
		parsed, err := url.Parse(redirect)
		if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1" || parsed.Hostname() == "localhost"))) {
			return storage.OAuthClient{}, errors.New("OAuth client redirect URI is invalid")
		}
	}
	return client, nil
}

func oauthClientDocument(client storage.OAuthClient) map[string]any {
	return map[string]any{"id": client.ID, "organizationId": client.OrganizationID, "name": client.Name, "public": client.Public, "redirectUris": client.RedirectURIs, "grantTypes": client.GrantTypes, "scopes": client.Scopes, "trusted": client.Trusted, "enabled": client.Enabled, "builtin": client.Builtin, "machineIdentityId": client.MachineIdentityID, "createdAt": client.CreatedAt, "updatedAt": client.UpdatedAt}
}
func generateOAuthClientSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("generate OAuth client secret")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (api *readAPI) oauthClientError(ctx *echo.Context, err error) error {
	status, code, message := http.StatusBadRequest, "invalid_request", "OAuth client request is invalid"
	if errors.Is(err, storage.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "OAuth client was not found"
	} else if errors.Is(err, storage.ErrConflict) {
		status, code, message = http.StatusConflict, "conflict", "OAuth client already exists"
	}
	writeError(ctx.Response(), status, code, message, requestID(ctx.Request()))
	return nil
}
