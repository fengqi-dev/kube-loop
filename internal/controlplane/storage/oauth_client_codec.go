package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

func scanOAuthClient(row rowScanner) (OAuthClient, error) {
	var c OAuthClient
	var redirects, grants, scopes, created, updated string
	var machine sql.NullString
	err := row.Scan(&c.ID, &c.Name, &c.Public, &redirects, &grants, &scopes,
		&c.Trusted, &c.Enabled, &c.Builtin, &machine, &created, &updated)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(redirects), &c.RedirectURIs); err != nil {
		return c, errors.New("decode OAuth client redirect URIs")
	}
	if err = json.Unmarshal([]byte(grants), &c.GrantTypes); err != nil {
		return c, errors.New("decode OAuth client grant types")
	}
	if err = json.Unmarshal([]byte(scopes), &c.Scopes); err != nil {
		return c, errors.New("decode OAuth client scopes")
	}
	c.MachineIdentityID = machine.String
	c.CreatedAt, err = parseTime(created, "OAuth client creation time")
	if err == nil {
		c.UpdatedAt, err = parseTime(updated, "OAuth client update time")
	}
	return c, err
}

func normalizeOAuthClient(client *OAuthClient, create bool) error {
	client.ID = strings.TrimSpace(client.ID)
	client.Name = strings.TrimSpace(client.Name)
	if client.ID == "" || len(client.ID) > 128 || client.Name == "" ||
		len(client.Name) > 128 {
		return errors.New("oAuth client identity is invalid")
	}
	for _, values := range [][]string{client.GrantTypes, client.Scopes} {
		if len(values) == 0 {
			return errors.New("oAuth client capabilities must not be empty")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return errors.New("oAuth client capability is invalid")
			}
		}
	}
	slices.Sort(client.RedirectURIs)
	client.RedirectURIs = slices.Compact(client.RedirectURIs)
	slices.Sort(client.GrantTypes)
	client.GrantTypes = slices.Compact(client.GrantTypes)
	for _, grant := range client.GrantTypes {
		if grant != grantAuthorizationCode && grant != grantRefreshToken &&
			grant != "client_credentials" {
			return errors.New("oAuth client grant type is not supported")
		}
	}
	if client.Public &&
		slices.Contains(client.GrantTypes, "client_credentials") {
		return errors.New("public OAuth clients cannot use client credentials")
	}
	if slices.Contains(client.GrantTypes, grantAuthorizationCode) &&
		len(client.RedirectURIs) == 0 {
		return errors.New(
			"authorization code OAuth clients require a redirect URI",
		)
	}
	if slices.Contains(client.GrantTypes, "client_credentials") {
		for _, scope := range []string{scopeOpenID, scopeProfile, emailField, scopeOfflineAccess} {
			if slices.Contains(client.Scopes, scope) {
				return errors.New(
					"client credentials OAuth clients cannot use identity scopes",
				)
			}
		}
	}
	slices.Sort(client.Scopes)
	client.Scopes = slices.Compact(client.Scopes)
	now := time.Now().UTC()
	if create && client.CreatedAt.IsZero() {
		client.CreatedAt = now
	}
	if client.UpdatedAt.IsZero() {
		client.UpdatedAt = now
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = client.UpdatedAt
	}
	return nil
}
