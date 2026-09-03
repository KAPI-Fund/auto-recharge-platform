package db

import (
	"testing"

	"github.com/kc-catk/auto-recharge-platform/backend/api/internal/models"
)

func TestProviderPlanNameForCodeMatchesLegacyCheckoutMapping(t *testing.T) {
	tests := map[string]string{
		"plus":            "chatgptplusplan",
		"pro_5x":          "chatgptprolite",
		"pro_20x":         "chatgptpro",
		"chatgptplusplan": "chatgptplusplan",
		"chatgptprolite":  "chatgptprolite",
		"chatgptpro":      "chatgptpro",
	}
	for input, want := range tests {
		if got := ProviderPlanNameForCode(input); got != want {
			t.Errorf("ProviderPlanNameForCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeProviderPlanNameRepairsLegacyStoredAliases(t *testing.T) {
	if got := NormalizeProviderPlanName("pro_20x", "pro_20x"); got != "chatgptpro" {
		t.Fatalf("normalized stored alias = %q, want chatgptpro", got)
	}
	if got := NormalizeProviderPlanName("pro_20x", ""); got != "chatgptpro" {
		t.Fatalf("normalized empty provider name = %q, want chatgptpro", got)
	}
	if got := NormalizeProviderPlanName("custom", "custom-plan"); got != "custom-plan" {
		t.Fatalf("custom override = %q, want custom-plan", got)
	}
}

func TestPlanTypeForPlanUsesCanonicalTypeForCustomStoreCodes(t *testing.T) {
	tests := []struct {
		name string
		plan models.Plan
		want string
	}{
		{
			name: "custom code linked to pro",
			plan: models.Plan{Code: "team-pro-monthly-2026", ProviderPlanName: "chatgptpro"},
			want: "pro_20x",
		},
		{
			name: "legacy short custom code",
			plan: models.Plan{Code: "legacy_custom"},
			want: "legacy_custom",
		},
		{
			name: "unmapped long code fallback",
			plan: models.Plan{Code: "custom-plan-code-that-is-longer-than-cdk-column"},
			want: "plus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanTypeForPlan(tt.plan); got != tt.want {
				t.Fatalf("PlanTypeForPlan(%+v) = %q, want %q", tt.plan, got, tt.want)
			}
		})
	}
}
