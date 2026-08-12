package development

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
)

const minimumStaticTokenBytes = 32

type IdentityConfig struct {
	Subject     string
	DisplayName string
	Email       string
	Groups      []string
}

type StaticToken struct {
	descriptor authn.Descriptor
	identity   authn.Identity
	tokenHash  [sha256.Size]byte
}

type Anonymous struct {
	descriptor authn.Descriptor
	identity   authn.Identity
}

var _ authn.TokenProvider = (*StaticToken)(nil)
var _ authn.AnonymousProvider = (*Anonymous)(nil)

func NewStaticToken(id, displayName string, rawToken []byte, identityConfig IdentityConfig) (*StaticToken, error) {
	defer clear(rawToken)
	rawToken = bytes.TrimSpace(rawToken)
	if len(rawToken) < minimumStaticTokenBytes {
		return nil, errors.New("development static token must contain at least 32 characters")
	}
	identity, err := developmentIdentity(id, identityConfig, "developer")
	if err != nil {
		return nil, err
	}
	return &StaticToken{
		descriptor: authn.Descriptor{
			ID: id, Type: authn.ProviderStaticToken, DisplayName: displayName, Interaction: authn.InteractionToken,
		},
		identity:  identity,
		tokenHash: sha256.Sum256(rawToken),
	}, nil
}

func NewAnonymous(id, displayName string, identityConfig IdentityConfig) (*Anonymous, error) {
	identity, err := developmentIdentity(id, identityConfig, "anonymous")
	if err != nil {
		return nil, err
	}
	return &Anonymous{
		descriptor: authn.Descriptor{
			ID: id, Type: authn.ProviderAnonymous, DisplayName: displayName, Interaction: authn.InteractionNone,
		},
		identity: identity,
	}, nil
}

func (provider *StaticToken) Descriptor() authn.Descriptor { return provider.descriptor }
func (provider *StaticToken) Check(context.Context) error  { return nil }

func (provider *StaticToken) AuthenticateToken(_ context.Context, credentials authn.TokenCredentials) (authn.Identity, error) {
	defer clear(credentials.Token)
	presented := sha256.Sum256(credentials.Token)
	if subtle.ConstantTimeCompare(presented[:], provider.tokenHash[:]) != 1 {
		return authn.Identity{}, errors.New("invalid development token")
	}
	return cloneIdentity(provider.identity), nil
}

func (provider *Anonymous) Descriptor() authn.Descriptor { return provider.descriptor }
func (provider *Anonymous) Check(context.Context) error  { return nil }
func (provider *Anonymous) AuthenticateAnonymous(context.Context) (authn.Identity, error) {
	return cloneIdentity(provider.identity), nil
}

func developmentIdentity(providerID string, config IdentityConfig, defaultSubject string) (authn.Identity, error) {
	providerID = strings.TrimSpace(providerID)
	config.Subject = strings.TrimSpace(config.Subject)
	if config.Subject == "" {
		config.Subject = defaultSubject
	}
	if len(config.Subject) > 256 {
		return authn.Identity{}, errors.New("development identity subject must not exceed 256 characters")
	}
	groups := make([]string, 0, len(config.Groups))
	seen := make(map[string]struct{}, len(config.Groups))
	for _, group := range config.Groups {
		group = strings.TrimSpace(group)
		if group == "" || len(group) > 256 {
			return authn.Identity{}, errors.New("development identity groups must be 1-256 characters")
		}
		if _, exists := seen[group]; exists {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	slices.Sort(groups)
	return authn.Identity{
		ProviderID: providerID, DevelopmentSubject: config.Subject,
		DisplayName: strings.TrimSpace(config.DisplayName), Email: strings.TrimSpace(config.Email), Groups: groups,
	}, nil
}

func cloneIdentity(identity authn.Identity) authn.Identity {
	identity.Groups = slices.Clone(identity.Groups)
	return identity
}
