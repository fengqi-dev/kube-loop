package remote

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
)

type capabilityAuthScope struct {
	ProfileID  string
	BaseURL    string
	Credential [sha256.Size]byte
}

type capabilityBinding struct {
	IdentityID     string
	GatewayVersion string
}

type capabilityCacheKey struct {
	Scope          capabilityAuthScope
	IdentityID     string
	Namespace      string
	GatewayVersion string
}

type capabilityCacheEntry struct {
	Value     Capabilities
	CachedAt  time.Time
	ExpiresAt time.Time
}

func (client *Client) Version(ctx context.Context, serverProfile profile.Profile) (Version, error) {
	var result Version
	if err := client.getJSON(ctx, serverProfile, "/api/version", nil, &result); err != nil {
		return Version{}, err
	}
	if strings.TrimSpace(result.GitVersion) == "" || strings.TrimSpace(result.GatewayVersion) == "" {
		return Version{}, errors.New("gateway returned an incomplete version document")
	}
	if scope, scopeErr := client.capabilityAuthScope(serverProfile); scopeErr == nil {
		client.bindGatewayVersion(scope, result.GatewayVersion)
	}
	return result, nil
}

func (client *Client) Capabilities(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace string,
) (Capabilities, error) {
	if err := validateNamespace(namespace); err != nil {
		return Capabilities{}, err
	}
	scope, err := client.capabilityAuthScope(serverProfile)
	if err != nil {
		return Capabilities{}, err
	}
	if cached, ok := client.cachedCapabilities(scope, namespace); ok {
		return cached, nil
	}
	var result Capabilities
	if err := client.getJSON(
		ctx,
		serverProfile,
		"/api/capabilities",
		url.Values{remoteParamNamespace: {namespace}},
		&result,
	); err != nil {
		return Capabilities{}, err
	}
	result, err = capability.Normalize(result)
	if err != nil || result.Namespace != namespace {
		return Capabilities{}, errors.New("gateway returned an invalid capability binding")
	}
	// The request may have rotated credentials. Bind the response only to the
	// authentication session that actually received it.
	scope, err = client.capabilityAuthScope(serverProfile)
	if err != nil {
		return Capabilities{}, err
	}
	client.storeCapabilities(scope, result)
	return cloneCapabilities(result), nil
}

func (client *Client) capabilityAuthScope(serverProfile profile.Profile) (capabilityAuthScope, error) {
	credential, err := client.credentials.Get(serverProfile.ID)
	if err != nil {
		return capabilityAuthScope{}, err
	}
	return capabilityAuthScope{
		ProfileID:  serverProfile.ID,
		BaseURL:    serverProfile.BaseURL,
		Credential: sha256.Sum256([]byte(credential.DeviceID + "\x00" + credential.RefreshToken)),
	}, nil
}

func (client *Client) bindGatewayVersion(scope capabilityAuthScope, gatewayVersion string) {
	client.capabilityMu.Lock()
	defer client.capabilityMu.Unlock()
	binding := client.capabilityBind[scope]
	if binding.GatewayVersion != "" && binding.GatewayVersion != gatewayVersion {
		client.evictCapabilityScopeLocked(scope)
	}
	binding.GatewayVersion = gatewayVersion
	client.capabilityBind[scope] = binding
}

func (client *Client) cachedCapabilities(scope capabilityAuthScope, namespace string) (Capabilities, bool) {
	client.capabilityMu.Lock()
	defer client.capabilityMu.Unlock()
	binding, ok := client.capabilityBind[scope]
	if !ok || binding.IdentityID == "" || binding.GatewayVersion == "" {
		return Capabilities{}, false
	}
	key := capabilityCacheKey{
		Scope: scope, IdentityID: binding.IdentityID, Namespace: namespace, GatewayVersion: binding.GatewayVersion,
	}
	entry, ok := client.capabilityCache[key]
	if !ok {
		return Capabilities{}, false
	}
	if !entry.ExpiresAt.After(client.now()) {
		delete(client.capabilityCache, key)
		return Capabilities{}, false
	}
	return cloneCapabilities(entry.Value), true
}

func (client *Client) storeCapabilities(scope capabilityAuthScope, value Capabilities) {
	now := client.now()
	client.capabilityMu.Lock()
	defer client.capabilityMu.Unlock()
	binding := capabilityBinding{IdentityID: value.IdentityID, GatewayVersion: value.GatewayVersion}
	if current, ok := client.capabilityBind[scope]; ok && current != binding {
		client.evictCapabilityScopeLocked(scope)
	}
	client.capabilityBind[scope] = binding
	for key, entry := range client.capabilityCache {
		if !entry.ExpiresAt.After(now) {
			delete(client.capabilityCache, key)
		}
	}
	for len(client.capabilityCache) >= client.capabilitySize {
		var oldestKey capabilityCacheKey
		var oldestTime time.Time
		for key, entry := range client.capabilityCache {
			if oldestTime.IsZero() || entry.CachedAt.Before(oldestTime) {
				oldestKey, oldestTime = key, entry.CachedAt
			}
		}
		delete(client.capabilityCache, oldestKey)
	}
	key := capabilityCacheKey{
		Scope: scope, IdentityID: value.IdentityID, Namespace: value.Namespace, GatewayVersion: value.GatewayVersion,
	}
	client.capabilityCache[key] = capabilityCacheEntry{
		Value: cloneCapabilities(value), CachedAt: now, ExpiresAt: now.Add(client.capabilityTTL),
	}
}

func (client *Client) evictCapabilityScopeLocked(scope capabilityAuthScope) {
	for key := range client.capabilityCache {
		if key.Scope == scope {
			delete(client.capabilityCache, key)
		}
	}
}

func cloneCapabilities(value Capabilities) Capabilities {
	value.Capabilities = append([]string(nil), value.Capabilities...)
	return value
}

func cloneCapabilityPointer(value *Capabilities) *Capabilities {
	if value == nil {
		return nil
	}
	cloned := cloneCapabilities(*value)
	return &cloned
}
