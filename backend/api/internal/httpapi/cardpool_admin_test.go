package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeCardPoolInputRejectsAmbiguousProviderConfiguration(t *testing.T) {
	cases := []struct {
		name  string
		input cardPoolInput
		want  string
	}{
		{
			name: "duplicate provider",
			input: cardPoolInput{Name: "duplicate", DefaultProvider: "LOCAL_TEXT", Providers: []cardPoolProviderInput{
				{Provider: "LOCAL_TEXT"}, {Provider: "local_text"},
			}},
			want: "不能重复",
		},
		{
			name: "default provider omitted",
			input: cardPoolInput{Name: "missing-default", DefaultProvider: "STRIPE_ISSUING", Providers: []cardPoolProviderInput{
				{Provider: "LOCAL_TEXT"},
			}},
			want: "必须包含",
		},
		{
			name: "default provider disabled",
			input: cardPoolInput{Name: "disabled-default", DefaultProvider: "LOCAL_TEXT", Providers: []cardPoolProviderInput{
				{Provider: "LOCAL_TEXT", Enabled: testBoolPointer(false)}, {Provider: "STRIPE_ISSUING"},
			}},
			want: "启用状态",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, _, err := normalizeCardPoolInput(item.input, "pool_test")
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("normalize error = %v, want text %q", err, item.want)
			}
		})
	}
}

func TestNormalizeCardPoolInputCanonicalizesValidProviderConfiguration(t *testing.T) {
	pool, providers, err := normalizeCardPoolInput(cardPoolInput{
		Name: "valid-pool", Type: "external_api", UsageType: "recurring", RoutingStrategy: "failover", DefaultProvider: "airwallex",
		Providers: []cardPoolProviderInput{
			{Provider: "AIRWALLEX", Priority: testIntPointer(1), Weight: testIntPointer(70)},
			{Provider: "stripe_issuing", Priority: testIntPointer(2), Weight: testIntPointer(30)},
		},
	}, "pool_test")
	if err != nil {
		t.Fatalf("valid provider configuration rejected: %v", err)
	}
	if pool.Type != "EXTERNAL_API" || pool.UsageType != "RECURRING" || pool.RoutingStrategy != "FAILOVER" || pool.DefaultProvider != "AIRWALLEX" {
		t.Fatalf("pool normalization = %+v", pool)
	}
	if len(providers) != 2 || providers[0].Provider != "AIRWALLEX" || providers[1].Provider != "STRIPE_ISSUING" || providers[1].Weight != 30 {
		t.Fatalf("provider normalization = %+v", providers)
	}
}

func TestNormalizeCardPoolInputAcceptsPhotonPayAndDogPay(t *testing.T) {
	pool, providers, err := normalizeCardPoolInput(cardPoolInput{
		Name: "issuing-pool", Type: "external_api", UsageType: "one_time", RoutingStrategy: "priority", DefaultProvider: "photonpay",
		Providers: []cardPoolProviderInput{
			{Provider: "photonpay", Priority: testIntPointer(1), Weight: testIntPointer(70)},
			{Provider: "dogpay", Priority: testIntPointer(2), Weight: testIntPointer(30)},
		},
	}, "pool_issuing")
	if err != nil {
		t.Fatalf("PhotonPay/DogPay provider configuration rejected: %v", err)
	}
	if pool.DefaultProvider != "PHOTONPAY" || len(providers) != 2 {
		t.Fatalf("normalized pool = %+v, providers = %+v", pool, providers)
	}
	if providers[0].Provider != "PHOTONPAY" || providers[1].Provider != "DOGPAY" {
		t.Fatalf("normalized providers = %+v", providers)
	}
}

func testBoolPointer(value bool) *bool { return &value }
func testIntPointer(value int) *int    { return &value }
