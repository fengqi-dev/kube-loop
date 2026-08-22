package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

func (client *Client) discoverProvider(ctx context.Context, baseURL string) (providerMetadata, error) {
	var metadata providerMetadata
	if err := client.getJSON(ctx, baseURL+"/.well-known/openid-configuration", &metadata); err != nil {
		return providerMetadata{}, err
	}
	if metadata.Issuer != baseURL || !sameOriginEndpoint(baseURL, metadata.AuthorizationEndpoint) ||
		!sameOriginEndpoint(
			baseURL,
			metadata.TokenEndpoint,
		) || !sameOriginEndpoint(baseURL, metadata.RevocationEndpoint) {
		return providerMetadata{}, errors.New("oIDC discovery returned invalid provider metadata")
	}
	return metadata, nil
}

func sameOriginEndpoint(baseURL, endpoint string) bool {
	base, baseErr := url.Parse(baseURL)
	target, targetErr := url.Parse(endpoint)
	return baseErr == nil && targetErr == nil && target.IsAbs() && target.User == nil && target.RawQuery == "" &&
		target.Fragment == "" && strings.EqualFold(base.Scheme, target.Scheme) && strings.EqualFold(base.Host, target.Host)
}
