package httpapi

import (
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/config"
)

func TestNewCardPoolServiceRegistersAllConfiguredProviders(t *testing.T) {
	service := NewCardPoolService(nil, config.Config{SessionEncryptionKey: "test-session-key"})
	if service == nil || service.Registry == nil {
		t.Fatal("card pool service registry is nil")
	}

	want := []string{"AIRWALLEX", "DOGPAY", "KIMOOX", "LOCAL_TEXT", "PHOTONPAY", "STRIPE_ISSUING"}
	got := service.Registry.Names()
	if len(got) != len(want) {
		t.Fatalf("registered providers = %#v, want %#v", got, want)
	}
	for index, name := range want {
		if got[index] != name {
			t.Fatalf("registered providers = %#v, want %#v", got, want)
		}
	}
}
