package cardpool

import (
	"sort"
	"strings"
)

type ProviderRegistry struct {
	providers map[string]CardProvider
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]CardProvider)}
}

func (r *ProviderRegistry) Register(provider CardProvider) {
	if r == nil || provider == nil {
		return
	}
	name := strings.ToUpper(strings.TrimSpace(provider.ProviderName()))
	if name == "" {
		return
	}
	if r.providers == nil {
		r.providers = make(map[string]CardProvider)
	}
	r.providers[name] = provider
}

func (r *ProviderRegistry) Get(name string) (CardProvider, error) {
	if r == nil {
		return nil, ErrProviderNotFound
	}
	provider, ok := r.providers[strings.ToUpper(strings.TrimSpace(name))]
	if !ok {
		return nil, ErrProviderNotFound
	}
	return provider, nil
}

func (r *ProviderRegistry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.providers[strings.ToUpper(strings.TrimSpace(name))]
	return ok
}

func (r *ProviderRegistry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
