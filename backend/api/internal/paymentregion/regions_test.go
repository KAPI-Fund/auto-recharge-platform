package paymentregion

import "testing"

func TestSupportedRegionsExposeStableCountryCurrencyContract(t *testing.T) {
	for _, code := range []string{"US", "PH", "IN", "AE", "DE", "JP", "ZA"} {
		region, ok := Get(code)
		if !ok {
			t.Fatalf("region %s is not registered", code)
		}
		if region.Code != code || region.Currency == "" || region.CountryLabel == "" || region.Locale == "" || region.Timezone == "" {
			t.Fatalf("region %s has incomplete contract: %#v", code, region)
		}
	}
}

func TestUnknownRegionDoesNotSilentlyBecomeSupported(t *testing.T) {
	if IsSupported("CN") {
		t.Fatal("CN must not be accepted until it is in the official supported billing list")
	}
	if Normalize(" unknown ") != "" {
		t.Fatal("unknown region normalized to a value")
	}
}

func TestAllReturnsCopy(t *testing.T) {
	first := All()
	first[0].Code = "MUTATED"
	second := All()
	if second[0].Code == "MUTATED" {
		t.Fatal("All exposed internal region state")
	}
}
