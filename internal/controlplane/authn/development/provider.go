package development

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
)

type IdentityConfig struct {
	Subject     string
	DisplayName string
	Email       string
	Groups      []string
}

type Anonymous struct {
	descriptor authn.Descriptor
	identity   authn.Identity
}

var _ authn.AnonymousProvider = (*Anonymous)(nil)

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
