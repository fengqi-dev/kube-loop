package authn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Registry struct {
	providers map[string]Provider
	ordered   []Descriptor
}

func NewRegistry(providers ...Provider) (*Registry, error) {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for index, provider := range providers {
		if provider == nil {
			return nil, fmt.Errorf("auth provider %d is nil", index)
		}
		descriptor := provider.Descriptor()
		descriptor.ID = strings.TrimSpace(descriptor.ID)
		descriptor.DisplayName = strings.TrimSpace(descriptor.DisplayName)
		if err := validateDescriptor(descriptor); err != nil {
			return nil, fmt.Errorf("auth provider %d: %w", index, err)
		}
		if _, exists := registry.providers[descriptor.ID]; exists {
			return nil, fmt.Errorf("duplicate auth provider ID %q", descriptor.ID)
		}
		registry.providers[descriptor.ID] = provider
		registry.ordered = append(registry.ordered, descriptor)
	}
	return registry, nil
}

func validateDescriptor(descriptor Descriptor) error {
	if !providerIDPattern.MatchString(descriptor.ID) {
		return errors.New("provider ID must be 1-128 URL-safe characters")
	}
	switch descriptor.Type {
	case ProviderOIDC:
		if descriptor.Interaction != InteractionBrowser {
			return errors.New("OIDC provider requires browser interaction")
		}
	case ProviderAD:
		if descriptor.Interaction != InteractionPassword {
			return errors.New("AD provider requires password interaction")
		}
	case ProviderStaticToken:
		if descriptor.Interaction != InteractionToken {
			return errors.New("static-token provider requires token interaction")
		}
	case ProviderAnonymous:
		if descriptor.Interaction != InteractionNone {
			return errors.New("anonymous provider requires none interaction")
		}
	default:
		return fmt.Errorf("unsupported provider type %q", descriptor.Type)
	}
	return nil
}

func (registry *Registry) Descriptors() []Descriptor {
	return slices.Clone(registry.ordered)
}

func (registry *Registry) Provider(id string) (Provider, bool) {
	provider, ok := registry.providers[id]
	return provider, ok
}

func (registry *Registry) Check(ctx context.Context) error {
	for _, descriptor := range registry.ordered {
		if err := registry.providers[descriptor.ID].Check(ctx); err != nil {
			return fmt.Errorf("auth provider %q unavailable: %w", descriptor.ID, err)
		}
	}
	return nil
}
