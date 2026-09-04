package region

import "testing"

func TestIndiaRegionUsesINR(t *testing.T) {
	cfg := Get("IN")
	if cfg.Currency != "INR" || cfg.Locale != "en-IN" || cfg.Timezone != "Asia/Kolkata" {
		t.Fatalf("IN config = %+v", cfg)
	}
}

func TestPlanAliasesResolveToCanonicalWorkerKeys(t *testing.T) {
	tests := map[string]struct {
		planType string
		wantType string
		wantName string
	}{
		"go":            {"go", "go", "chatgptgoplan"},
		"legacy go":     {"chatgptgoplan", "go", "chatgptgoplan"},
		"underscore go": {"chatgpt_go", "go", "chatgptgoplan"},
		"plus":          {"plus", "plus", "chatgptplusplan"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := NormalizePlanType(tc.planType); got != tc.wantType {
				t.Fatalf("NormalizePlanType(%q) = %q, want %q", tc.planType, got, tc.wantType)
			}
			if got := PlanName(tc.planType); got != tc.wantName {
				t.Fatalf("PlanName(%q) = %q, want %q", tc.planType, got, tc.wantName)
			}
			if got := NormalizePlanName(tc.planType, ""); got != tc.wantName {
				t.Fatalf("NormalizePlanName(%q, empty) = %q, want %q", tc.planType, got, tc.wantName)
			}
		})
	}
}
