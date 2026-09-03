package httpapi

import "testing"

func TestSystemMetricPercentClamps(t *testing.T) {
	for value, want := range map[float64]int{-1: 0, 0: 0, 42: 42, 101: 100} {
		if got := clampSystemPercent(value); got != want {
			t.Fatalf("clampSystemPercent(%v) = %v, want %v", value, got, want)
		}
	}
}

func TestSystemMetricSnapshotHasDashboardFields(t *testing.T) {
	data := collectSystemMetrics()
	for _, key := range []string{"cpu", "memory", "disk", "uptime"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("system metrics missing %q: %#v", key, data)
		}
	}
	if _, ok := data["cpu"].(map[string]any)["percent"]; !ok {
		t.Fatalf("cpu percent missing: %#v", data["cpu"])
	}
	if _, ok := data["memory"].(map[string]any)["text"]; !ok {
		t.Fatalf("memory text missing: %#v", data["memory"])
	}
	if got := data["disk"].(map[string]any)["drive"]; got != "/" {
		t.Fatalf("disk drive = %v, want /", got)
	}
}

func TestFormatSystemBytes(t *testing.T) {
	if got := formatSystemBytes(1024 * 1024 * 1024); got != "1.0G" {
		t.Fatalf("formatSystemBytes = %q, want 1.0G", got)
	}
}
