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
		"go":              "chatgptgoplan",
		"chatgptplusplan": "chatgptplusplan",
		"chatgptprolite":  "chatgptprolite",
		"chatgptpro":      "chatgptpro",
		"chatgptgoplan":   "chatgptgoplan",
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

func TestPrepaidRechargeUSDForPlanAddsFiveDollarBuffer(t *testing.T) {
	tests := []struct {
		plan models.Plan
		want float64
		ok   bool
	}{
		{plan: models.Plan{Code: "plus"}, want: 25, ok: true},
		{plan: models.Plan{Code: "pro_5x"}, want: 105, ok: true},
		{plan: models.Plan{Code: "pro_20x"}, want: 205, ok: true},
		{plan: models.Plan{Code: "shop-pro", ProviderPlanName: "chatgptpro"}, want: 205, ok: true},
		{plan: models.Plan{Code: "go"}, want: 0, ok: false},
	}
	for _, tt := range tests {
		got, ok := PrepaidRechargeUSDForPlan(tt.plan)
		if ok != tt.ok || got != tt.want {
			t.Fatalf("PrepaidRechargeUSDForPlan(%+v) = (%v, %v), want (%v, %v)", tt.plan, got, ok, tt.want, tt.ok)
		}
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
