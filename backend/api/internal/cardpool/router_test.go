package cardpool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type routerProvider struct {
	name   string
	create atomic.Int32
	err    error
}

func (p *routerProvider) ProviderName() string { return p.name }
func (p *routerProvider) HealthCheck(context.Context) (ProviderHealth, error) {
	return ProviderHealth{Provider: p.name, Available: true}, nil
}
func (p *routerProvider) AcquireCard(context.Context, AcquireCardRequest) (PaymentCard, error) {
	p.create.Add(1)
	return PaymentCard{Provider: p.name, ProviderCardID: p.name + "-card", Status: CardActive}, p.err
}
func (p *routerProvider) CreateCard(context.Context, CreateCardRequest) (PaymentCard, error) {
	return PaymentCard{}, nil
}
func (p *routerProvider) GetCard(context.Context, string) (PaymentCard, error) {
	return PaymentCard{}, nil
}
func (p *routerProvider) GetSensitiveCardDetails(context.Context, string) (SensitiveCardDetails, error) {
	return SensitiveCardDetails{}, nil
}
func (p *routerProvider) FreezeCard(context.Context, string) error   { return nil }
func (p *routerProvider) UnfreezeCard(context.Context, string) error { return nil }
func (p *routerProvider) CancelCard(context.Context, string) error   { return nil }
func (p *routerProvider) UpdateLimits(context.Context, string, CardLimits) error {
	return nil
}
func (p *routerProvider) GetTransactions(context.Context, string) ([]CardTransaction, error) {
	return nil, nil
}
func (p *routerProvider) Supports(Capability) bool { return true }

func TestRouterOnlyFailsOverForTechnicalProviderErrors(t *testing.T) {
	registry := NewProviderRegistry()
	primary := &routerProvider{name: "PRIMARY", err: NewProviderError("PRIMARY", "create", CategoryBusinessDecline, false, true, errors.New("declined"))}
	backup := &routerProvider{name: "BACKUP"}
	registry.Register(primary)
	registry.Register(backup)
	router := NewCardProviderRouter(registry)
	routes := []ProviderRoute{{Provider: "PRIMARY", Enabled: true, Priority: 1}, {Provider: "BACKUP", Enabled: true, Priority: 2}}
	err := router.Execute(RoutingFailover, "PRIMARY", routes, func(provider CardProvider) error {
		_, err := provider.AcquireCard(context.Background(), AcquireCardRequest{})
		return err
	})
	if err == nil || primary.create.Load() != 1 || backup.create.Load() != 0 {
		t.Fatalf("business decline unexpectedly failed over: err=%v primary=%d backup=%d", err, primary.create.Load(), backup.create.Load())
	}

	primary.err = NewProviderError("PRIMARY", "create", CategoryProviderUnavailable, true, true, errors.New("timeout"))
	err = router.Execute(RoutingFailover, "PRIMARY", routes, func(provider CardProvider) error {
		_, err := provider.AcquireCard(context.Background(), AcquireCardRequest{})
		return err
	})
	if err != nil || backup.create.Load() != 1 {
		t.Fatalf("technical failure did not fail over: err=%v backup=%d", err, backup.create.Load())
	}
}

func TestRouterExecuteSelectedKeepsWeightedAllocationProvider(t *testing.T) {
	primary := &routerProvider{name: "PRIMARY"}
	backup := &routerProvider{name: "BACKUP"}
	registry := NewProviderRegistry()
	registry.Register(primary)
	registry.Register(backup)
	router := NewCardProviderRouter(registry)
	routes := []ProviderRoute{
		{Provider: "PRIMARY", Enabled: true, Priority: 1, Weight: 70},
		{Provider: "BACKUP", Enabled: true, Priority: 2, Weight: 30},
	}
	selected, err := router.Select(RoutingWeighted, "", routes)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	called := ""
	err = router.ExecuteSelected(RoutingWeighted, "", routes, selected.ProviderName(), func(provider CardProvider) error {
		called = provider.ProviderName()
		return nil
	})
	if err != nil {
		t.Fatalf("ExecuteSelected() error = %v", err)
	}
	if called != selected.ProviderName() {
		t.Fatalf("weighted provider changed between allocation and execution: selected=%s called=%s", selected.ProviderName(), called)
	}
}

func TestRouterExecuteSelectedKeepsPersistedProviderAfterPoolEdit(t *testing.T) {
	primary := &routerProvider{name: "PRIMARY"}
	backup := &routerProvider{name: "BACKUP"}
	registry := NewProviderRegistry()
	registry.Register(primary)
	registry.Register(backup)
	router := NewCardProviderRouter(registry)

	// PRIMARY was persisted on the allocation before the pool was edited and
	// removed from the current route list. It must still receive the create
	// operation; BACKUP is only a later failover candidate.
	called := ""
	err := router.ExecuteSelected(RoutingFailover, "BACKUP", []ProviderRoute{{Provider: "BACKUP", Enabled: true, Priority: 1}}, "PRIMARY", func(provider CardProvider) error {
		called = provider.ProviderName()
		return nil
	})
	if err != nil || called != "PRIMARY" {
		t.Fatalf("persisted provider was redirected after pool edit: err=%v called=%s", err, called)
	}
}

func TestRouterExecuteSelectedRejectsUnknownPersistedProvider(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&routerProvider{name: "BACKUP"})
	router := NewCardProviderRouter(registry)
	err := router.ExecuteSelected(RoutingFailover, "BACKUP", []ProviderRoute{{Provider: "BACKUP", Enabled: true}}, "REMOVED", func(CardProvider) error {
		return nil
	})
	if !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("unknown persisted provider error = %v, want ErrProviderNotFound", err)
	}
}

func TestRouterFixedDoesNotTryBackup(t *testing.T) {
	registry := NewProviderRegistry()
	primary := &routerProvider{name: "PRIMARY", err: NewProviderError("PRIMARY", "create", CategoryProviderUnavailable, true, true, errors.New("down"))}
	backup := &routerProvider{name: "BACKUP"}
	registry.Register(primary)
	registry.Register(backup)
	router := NewCardProviderRouter(registry)
	err := router.Execute(RoutingFixed, "PRIMARY", []ProviderRoute{{Provider: "PRIMARY", Enabled: true}, {Provider: "BACKUP", Enabled: true}}, func(provider CardProvider) error {
		_, err := provider.AcquireCard(context.Background(), AcquireCardRequest{})
		return err
	})
	if err == nil || primary.create.Load() != 1 || backup.create.Load() != 0 {
		t.Fatalf("fixed routing tried backup: err=%v primary=%d backup=%d", err, primary.create.Load(), backup.create.Load())
	}
}
