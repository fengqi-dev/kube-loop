// Package provider builds and atomically installs database-managed OIDC/AD
// Provider revisions without accepting Secret plaintext or arbitrary file paths.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	adminrevision "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/revision"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	authconfig "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/config"
	oidcprovider "github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oidc"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const DefaultReloadInterval = 2 * time.Second

var ErrUnavailable = errors.New("managed authentication Providers are unavailable")

type SecretResolver interface {
	Resolve(alias, use string) (string, error)
}

type OIDCConfig struct {
	DisplayName        string                    `json:"displayName,omitempty"`
	Issuer             string                    `json:"issuer"`
	ClientID           string                    `json:"clientId"`
	Scopes             []string                  `json:"scopes,omitempty"`
	AllowedSigningAlgs []string                  `json:"allowedSigningAlgs,omitempty"`
	RequiredClaims     []string                  `json:"requiredClaims,omitempty"`
	Claims             oidcprovider.ClaimMapping `json:"claims"`
	HTTPTimeout        string                    `json:"httpTimeout,omitempty"`
	Enabled            *bool                     `json:"enabled,omitempty"`
}

type ADConfig struct {
	DisplayName                 string `json:"displayName,omitempty"`
	DirectoryID                 string `json:"directoryId"`
	URL                         string `json:"url"`
	StartTLS                    bool   `json:"startTLS,omitempty"`
	BaseDN                      string `json:"baseDn"`
	UserFilter                  string `json:"userFilter,omitempty"`
	BindDN                      string `json:"bindDn,omitempty"`
	ObjectIDAttribute           string `json:"objectIdAttribute,omitempty"`
	DisplayNameAttribute        string `json:"displayNameAttribute,omitempty"`
	EmailAttribute              string `json:"emailAttribute,omitempty"`
	GroupsAttribute             string `json:"groupsAttribute,omitempty"`
	UserAccountControlAttribute string `json:"userAccountControlAttribute,omitempty"`
	LockoutTimeAttribute        string `json:"lockoutTimeAttribute,omitempty"`
	AccountExpiresAttribute     string `json:"accountExpiresAttribute,omitempty"`
	PasswordLastSetAttribute    string `json:"passwordLastSetAttribute,omitempty"`
	GroupNameAttribute          string `json:"groupNameAttribute,omitempty"`
	NestedGroupDepth            int    `json:"nestedGroupDepth,omitempty"`
	MaxGroups                   int    `json:"maxGroups,omitempty"`
	ConnectTimeout              string `json:"connectTimeout,omitempty"`
	RequestTimeout              string `json:"requestTimeout,omitempty"`
	Enabled                     *bool  `json:"enabled,omitempty"`
}

type Runtime struct {
	repositories storage.Repositories
	registry     *authn.Registry
	baseline     authconfig.File
	secrets      SecretResolver
	publicURL    string
	interval     time.Duration
	apply        sync.Mutex

	mu      sync.RWMutex
	lastErr error
	loaded  string
}

func NewRuntime(
	repositories storage.Repositories,
	registry *authn.Registry,
	baseline authconfig.File,
	secrets SecretResolver,
	publicURL string,
	interval time.Duration,
) (*Runtime, error) {
	if repositories == nil || registry == nil || secrets == nil {
		return nil, errors.New("managed Provider runtime dependencies are required")
	}
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(publicURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("managed Provider public URL is invalid")
	}
	if interval == 0 {
		interval = DefaultReloadInterval
	}
	if interval < 100*time.Millisecond || interval > time.Minute {
		return nil, errors.New("managed Provider reload interval must be between 100ms and 1m")
	}
	return &Runtime{repositories: repositories, registry: registry, baseline: cloneFile(baseline), secrets: secrets,
		publicURL: parsed.String(), interval: interval}, nil
}

func (runtime *Runtime) Validate(ctx context.Context, candidate adminrevision.ProviderCandidate) (json.RawMessage, error) {
	provider, enabled, err := runtime.provider(candidate)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return json.RawMessage(`{"valid":true,"enabled":false}`), nil
	}
	registry, err := authconfig.Build(ctx, authconfig.File{Providers: []authconfig.Provider{provider}})
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := registry.Check(ctx); err != nil {
		return nil, ErrUnavailable
	}
	return json.RawMessage(`{"valid":true,"connectivity":"ready"}`), nil
}

func (runtime *Runtime) Prepare(ctx context.Context, candidate adminrevision.ProviderCandidate) (func(), error) {
	next, err := runtime.build(ctx, &candidate)
	if err != nil {
		return nil, err
	}
	_, enabled := next.Provider(candidate.ID)
	return func() {
		runtime.apply.Lock()
		defer runtime.apply.Unlock()
		if err := runtime.registry.ReplaceProvider(next, candidate.ID, enabled); err != nil {
			runtime.setError(ErrUnavailable)
			return
		}
		runtime.setError(nil)
		runtime.setLoaded("")
	}, nil
}

func (runtime *Runtime) Load(ctx context.Context) error {
	runtime.apply.Lock()
	defer runtime.apply.Unlock()
	fingerprint, err := runtime.activeFingerprint(ctx)
	if err != nil {
		runtime.setError(err)
		return err
	}
	if runtime.isLoaded(fingerprint) {
		runtime.setError(nil)
		return nil
	}
	next, err := runtime.build(ctx, nil)
	if err != nil {
		runtime.setError(err)
		return err
	}
	if err := runtime.registry.Replace(next); err != nil {
		runtime.setError(ErrUnavailable)
		return ErrUnavailable
	}
	runtime.setError(nil)
	runtime.setLoaded(fingerprint)
	return nil
}

func (runtime *Runtime) Run(ctx context.Context) {
	ticker := time.NewTicker(runtime.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = runtime.Load(ctx)
		}
	}
}

func (runtime *Runtime) Check(context.Context) error {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.lastErr
}

func (runtime *Runtime) build(ctx context.Context, override *adminrevision.ProviderCandidate) (*authn.Registry, error) {
	providers := make(map[string]authconfig.Provider, len(runtime.baseline.Providers)+1)
	for _, item := range runtime.baseline.Providers {
		providers[item.ID] = item
	}
	pointers, err := runtime.repositories.ActiveManagementRevisions().List(ctx, storage.ManagementConfigurationProvider)
	if err != nil {
		return nil, ErrUnavailable
	}
	for _, pointer := range pointers {
		if override != nil && pointer.ConfigurationID == override.ID {
			continue
		}
		revision, err := runtime.repositories.ProviderConfigRevisions().Get(ctx, pointer.Revision)
		if err != nil || revision.ProviderID != pointer.ConfigurationID || revision.ValidationState != storage.RevisionValidationValid {
			return nil, ErrUnavailable
		}
		item, enabled, err := runtime.provider(adminrevision.ProviderCandidate{
			ID: revision.ProviderID, Type: revision.ProviderType, Config: revision.Config, SecretAliases: revision.SecretAliases,
		})
		if err != nil {
			return nil, err
		}
		if enabled {
			providers[item.ID] = item
		} else {
			delete(providers, item.ID)
		}
	}
	if override != nil {
		item, enabled, err := runtime.provider(*override)
		if err != nil {
			return nil, err
		}
		if enabled {
			providers[item.ID] = item
		} else {
			delete(providers, item.ID)
		}
	}
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	file := authconfig.File{DevelopmentMode: runtime.baseline.DevelopmentMode, Providers: make([]authconfig.Provider, 0, len(ids))}
	for _, id := range ids {
		file.Providers = append(file.Providers, providers[id])
	}
	registry, err := authconfig.Build(ctx, file)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := registry.Check(ctx); err != nil {
		return nil, ErrUnavailable
	}
	return registry, nil
}

func (runtime *Runtime) provider(candidate adminrevision.ProviderCandidate) (authconfig.Provider, bool, error) {
	aliases := make(map[string]string)
	if err := decodeStrict(candidate.SecretAliases, &aliases); err != nil {
		return authconfig.Provider{}, false, err
	}
	switch candidate.Type {
	case "oidc":
		var config OIDCConfig
		if err := decodeStrict(candidate.Config, &config); err != nil {
			return authconfig.Provider{}, false, err
		}
		enabled := config.Enabled == nil || *config.Enabled
		if !enabled {
			return authconfig.Provider{ID: candidate.ID}, false, nil
		}
		if err := exactAliases(aliases, []string{"client-secret"}, []string{"ca"}); err != nil {
			return authconfig.Provider{}, false, err
		}
		secretFile, err := runtime.secrets.Resolve(aliases["client-secret"], "client-secret")
		if err != nil {
			return authconfig.Provider{}, false, ErrUnavailable
		}
		caFile := ""
		if aliases["ca"] != "" {
			caFile, err = runtime.secrets.Resolve(aliases["ca"], "ca")
			if err != nil {
				return authconfig.Provider{}, false, ErrUnavailable
			}
		}
		return authconfig.Provider{ID: candidate.ID, Type: "oidc", DisplayName: config.DisplayName, OIDC: &authconfig.OIDCConfig{
			Issuer: config.Issuer, ClientID: config.ClientID, ClientSecretFile: secretFile,
			RedirectURL: runtime.publicURL + "/auth/callback/" + url.PathEscape(candidate.ID),
			Scopes:      config.Scopes, AllowedSigningAlgs: config.AllowedSigningAlgs,
			RequiredClaims: config.RequiredClaims, Claims: config.Claims, CAFile: caFile, HTTPTimeout: config.HTTPTimeout,
		}}, true, nil
	case "ad":
		var config ADConfig
		if err := decodeStrict(candidate.Config, &config); err != nil {
			return authconfig.Provider{}, false, err
		}
		enabled := config.Enabled == nil || *config.Enabled
		if !enabled {
			return authconfig.Provider{ID: candidate.ID}, false, nil
		}
		required := []string{}
		if strings.TrimSpace(config.BindDN) != "" {
			required = append(required, "bind-password")
		}
		if err := exactAliases(aliases, required, []string{"bind-password", "ca"}); err != nil {
			return authconfig.Provider{}, false, err
		}
		bindPasswordFile, caFile := "", ""
		var err error
		if aliases["bind-password"] != "" {
			bindPasswordFile, err = runtime.secrets.Resolve(aliases["bind-password"], "bind-password")
			if err != nil {
				return authconfig.Provider{}, false, ErrUnavailable
			}
		}
		if aliases["ca"] != "" {
			caFile, err = runtime.secrets.Resolve(aliases["ca"], "ca")
			if err != nil {
				return authconfig.Provider{}, false, ErrUnavailable
			}
		}
		return authconfig.Provider{ID: candidate.ID, Type: "ad", DisplayName: config.DisplayName, AD: &authconfig.ADConfig{
			DirectoryID: config.DirectoryID, URL: config.URL, StartTLS: config.StartTLS, BaseDN: config.BaseDN,
			UserFilter: config.UserFilter, BindDN: config.BindDN, BindPasswordFile: bindPasswordFile, CAFile: caFile,
			ObjectIDAttribute: config.ObjectIDAttribute, DisplayNameAttribute: config.DisplayNameAttribute,
			EmailAttribute: config.EmailAttribute, GroupsAttribute: config.GroupsAttribute,
			UserAccountControlAttribute: config.UserAccountControlAttribute, LockoutTimeAttribute: config.LockoutTimeAttribute,
			AccountExpiresAttribute: config.AccountExpiresAttribute, PasswordLastSetAttribute: config.PasswordLastSetAttribute,
			GroupNameAttribute: config.GroupNameAttribute, NestedGroupDepth: config.NestedGroupDepth, MaxGroups: config.MaxGroups,
			ConnectTimeout: config.ConnectTimeout, RequestTimeout: config.RequestTimeout,
		}}, true, nil
	default:
		return authconfig.Provider{}, false, errors.New("unsupported managed Provider type")
	}
}

func decodeStrict(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("managed Provider configuration is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("managed Provider configuration has trailing content")
	}
	return nil
}

func exactAliases(aliases map[string]string, required, optional []string) error {
	allowed := make(map[string]struct{}, len(required)+len(optional))
	for _, use := range append(append([]string(nil), required...), optional...) {
		allowed[use] = struct{}{}
	}
	for use, alias := range aliases {
		if _, ok := allowed[use]; !ok || strings.TrimSpace(alias) == "" {
			return fmt.Errorf("managed Provider Secret alias use %q is invalid", use)
		}
	}
	for _, use := range required {
		if strings.TrimSpace(aliases[use]) == "" {
			return fmt.Errorf("managed Provider Secret alias %q is required", use)
		}
	}
	return nil
}

func cloneFile(source authconfig.File) authconfig.File {
	encoded, _ := json.Marshal(source)
	var result authconfig.File
	_ = json.Unmarshal(encoded, &result)
	return result
}

func (runtime *Runtime) setError(err error) {
	runtime.mu.Lock()
	runtime.lastErr = err
	runtime.mu.Unlock()
}

func (runtime *Runtime) activeFingerprint(ctx context.Context) (string, error) {
	pointers, err := runtime.repositories.ActiveManagementRevisions().List(ctx, storage.ManagementConfigurationProvider)
	if err != nil {
		return "", ErrUnavailable
	}
	var value strings.Builder
	for _, pointer := range pointers {
		fmt.Fprintf(&value, "%d:%s:%d:%d;", len(pointer.ConfigurationID), pointer.ConfigurationID, pointer.Revision, pointer.ETag)
	}
	return value.String(), nil
}

func (runtime *Runtime) isLoaded(fingerprint string) bool {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.loaded == fingerprint
}

func (runtime *Runtime) setLoaded(fingerprint string) {
	runtime.mu.Lock()
	runtime.loaded = fingerprint
	runtime.mu.Unlock()
}

var (
	_ adminrevision.ProviderValidator = (*Runtime)(nil)
	_ adminrevision.ProviderActivator = (*Runtime)(nil)
)
