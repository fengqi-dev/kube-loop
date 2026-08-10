package authn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
)

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Registry struct {
	current atomic.Pointer[registrySnapshot]
}

type registrySnapshot struct {
	providers map[string]Provider
	ordered   []Descriptor
}

func NewRegistry(providers ...Provider) (*Registry, error) {
	snapshot, err := newRegistrySnapshot(providers)
	if err != nil {
		return nil, err
	}
	registry := &Registry{}
	registry.current.Store(snapshot)
	return registry, nil
}

func newRegistrySnapshot(providers []Provider) (*registrySnapshot, error) {
	snapshot := &registrySnapshot{providers: make(map[string]Provider, len(providers))}
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
		if _, exists := snapshot.providers[descriptor.ID]; exists {
			return nil, fmt.Errorf("duplicate auth provider ID %q", descriptor.ID)
		}
		snapshot.providers[descriptor.ID] = provider
		snapshot.ordered = append(snapshot.ordered, descriptor)
	}
	return snapshot, nil
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
	snapshot := registry.snapshot()
	if snapshot == nil {
		return []Descriptor{}
	}
	return slices.Clone(snapshot.ordered)
}

func (registry *Registry) Provider(id string) (Provider, bool) {
	snapshot := registry.snapshot()
	if snapshot == nil {
		return nil, false
	}
	provider, ok := snapshot.providers[id]
	return provider, ok
}

func (registry *Registry) Check(ctx context.Context) error {
	snapshot := registry.snapshot()
	if snapshot == nil {
		return errors.New("auth provider registry is unavailable")
	}
	for _, descriptor := range snapshot.ordered {
		if err := snapshot.providers[descriptor.ID].Check(ctx); err != nil {
			return fmt.Errorf("auth provider %q unavailable: %w", descriptor.ID, err)
		}
	}
	return nil
}

// Replace atomically installs a fully validated registry snapshot. In-flight
// requests retain their already-resolved Provider; subsequent login,
// discovery and readiness reads observe the new set without a partial state.
func (registry *Registry) Replace(next *Registry) error {
	if registry == nil || next == nil || next.snapshot() == nil {
		return errors.New("auth provider registry replacement is invalid")
	}
	registry.current.Store(next.snapshot())
	return nil
}

// ReplaceProvider atomically applies only one Provider from a prepared
// Registry. Unrelated Providers already installed by concurrent publications
// are preserved instead of being overwritten by an older aggregate snapshot.
func (registry *Registry) ReplaceProvider(prepared *Registry, providerID string, enabled bool) error {
	if registry == nil || prepared == nil || registry.current.Load() == nil {
		return errors.New("authentication Provider Registry is not initialized")
	}
	var replacement Provider
	if enabled {
		var ok bool
		replacement, ok = prepared.Provider(providerID)
		if !ok {
			return errors.New("prepared authentication Provider is missing")
		}
	}
	for {
		active := registry.current.Load()
		providers := make(map[string]Provider, len(active.providers)+1)
		for id, provider := range active.providers {
			providers[id] = provider
		}
		if enabled {
			providers[providerID] = replacement
		} else {
			delete(providers, providerID)
		}
		ordered := make([]Descriptor, 0, len(providers))
		for _, provider := range providers {
			ordered = append(ordered, provider.Descriptor())
		}
		sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
		if registry.current.CompareAndSwap(active, &registrySnapshot{providers: providers, ordered: ordered}) {
			return nil
		}
	}
}

func (registry *Registry) snapshot() *registrySnapshot {
	if registry == nil {
		return nil
	}
	return registry.current.Load()
}
