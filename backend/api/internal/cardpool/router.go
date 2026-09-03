package cardpool

import (
	"math/rand"
	"sort"
	"strings"
	"sync"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

type ProviderRoute struct {
	Provider string
	Enabled  bool
	Priority int
	Weight   int
}

type CardProviderRouter struct {
	Registry *ProviderRegistry
	random   *rand.Rand
	mu       sync.Mutex
}

func NewCardProviderRouter(registry *ProviderRegistry) *CardProviderRouter {
	return &CardProviderRouter{Registry: registry, random: rand.New(rand.NewSource(1))}
}

func (r *CardProviderRouter) candidates(strategy RoutingStrategy, defaultProvider string, routes []ProviderRoute) ([]CardProvider, error) {
	available := make([]ProviderRoute, 0, len(routes))
	for _, route := range routes {
		if !route.Enabled || strings.TrimSpace(route.Provider) == "" {
			continue
		}
		if route.Priority <= 0 {
			route.Priority = 1
		}
		if route.Weight < 0 {
			route.Weight = 0
		}
		available = append(available, route)
	}
	if len(available) == 0 {
		return nil, ErrProviderNotFound
	}
	sort.SliceStable(available, func(i, j int) bool {
		if available[i].Priority != available[j].Priority {
			return available[i].Priority < available[j].Priority
		}
		return strings.ToUpper(available[i].Provider) < strings.ToUpper(available[j].Provider)
	})
	if strategy == RoutingFixed {
		want := strings.ToUpper(strings.TrimSpace(defaultProvider))
		if want == "" {
			want = strings.ToUpper(strings.TrimSpace(available[0].Provider))
		}
		for _, route := range available {
			if strings.ToUpper(route.Provider) == want {
				return r.resolve([]ProviderRoute{route})
			}
		}
		return nil, ErrProviderNotFound
	}
	if strategy == RoutingWeighted {
		selected := weightedPick(available, r)
		ordered := []ProviderRoute{selected}
		for _, route := range available {
			if strings.ToUpper(route.Provider) != strings.ToUpper(selected.Provider) {
				ordered = append(ordered, route)
			}
		}
		return r.resolve(ordered)
	}
	return r.resolve(available)
}

func weightedPick(routes []ProviderRoute, router *CardProviderRouter) ProviderRoute {
	total := 0
	for _, route := range routes {
		total += route.Weight
	}
	if total <= 0 {
		return routes[0]
	}
	router.mu.Lock()
	pick := router.random.Intn(total)
	router.mu.Unlock()
	for _, route := range routes {
		if pick < route.Weight {
			return route
		}
		pick -= route.Weight
	}
	return routes[len(routes)-1]
}

func (r *CardProviderRouter) resolve(routes []ProviderRoute) ([]CardProvider, error) {
	providers := make([]CardProvider, 0, len(routes))
	for _, route := range routes {
		provider, err := r.Registry.Get(route.Provider)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func (r *CardProviderRouter) Select(strategy RoutingStrategy, defaultProvider string, routes []ProviderRoute) (CardProvider, error) {
	providers, err := r.candidates(strategy, defaultProvider, routes)
	if err != nil {
		return nil, err
	}
	return providers[0], nil
}

// Execute only falls through for explicit FAILOVER routing and errors marked
// technical/provider-unavailable by an adapter. Merchant declines and funds or
// compliance failures never silently switch providers.
func (r *CardProviderRouter) Execute(strategy RoutingStrategy, defaultProvider string, routes []ProviderRoute, fn func(CardProvider) error) error {
	return r.execute(strategy, defaultProvider, routes, "", fn)
}

// ExecuteSelected starts with the provider already persisted for the
// allocation. This matters for WEIGHTED routing: selecting once before the
// database row is created and selecting again during execution would draw two
// random values and could send the request to a different provider than the
// allocation metadata says.
func (r *CardProviderRouter) ExecuteSelected(strategy RoutingStrategy, defaultProvider string, routes []ProviderRoute, selectedProvider string, fn func(CardProvider) error) error {
	return r.execute(strategy, defaultProvider, routes, selectedProvider, fn)
}

func (r *CardProviderRouter) execute(strategy RoutingStrategy, defaultProvider string, routes []ProviderRoute, selectedProvider string, fn func(CardProvider) error) error {
	providers, err := r.candidates(strategy, defaultProvider, routes)
	if err != nil {
		return err
	}
	if selected := strings.ToUpper(strings.TrimSpace(selectedProvider)); selected != "" {
		// The provider persisted on an allocation is authoritative. A pool can
		// be edited after allocation creation; that must not silently redirect
		// the in-flight create operation to a different provider. Keep the
		// persisted provider first and append only the currently configured
		// providers as failover candidates.
		persisted, lookupErr := r.Registry.Get(selected)
		if lookupErr != nil {
			return lookupErr
		}
		ordered := []CardProvider{persisted}
		for _, provider := range providers {
			if !strings.EqualFold(provider.ProviderName(), selected) {
				ordered = append(ordered, provider)
			}
		}
		providers = ordered
	}
	var last error
	for index, provider := range providers {
		last = fn(provider)
		if last == nil {
			return nil
		}
		if strategy != RoutingFailover || index == len(providers)-1 || !IsFailoverEligible(last) {
			return last
		}
	}
	return last
}

func routeModels(pool models.CardPool) []ProviderRoute {
	routes := make([]ProviderRoute, 0, len(pool.Providers))
	for _, item := range pool.Providers {
		routes = append(routes, ProviderRoute{Provider: item.Provider, Enabled: item.Enabled, Priority: item.Priority, Weight: item.Weight})
	}
	return routes
}
