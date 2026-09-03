package config

import "testing"

func TestEnvBoolAcceptsDeploymentBooleanValues(t *testing.T) {
	tests := []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"1", false, true}, {"true", false, true}, {"yes", false, true}, {"on", false, true},
		{"0", true, false}, {"false", true, false}, {"no", true, false}, {"off", true, false},
		{"unknown", true, true}, {"unknown", false, false},
	}
	for _, test := range tests {
		t.Setenv("KC_TEST_BOOL", test.value)
		if got := envBool("KC_TEST_BOOL", test.fallback); got != test.want {
			t.Errorf("envBool(%q, %v) = %v, want %v", test.value, test.fallback, got, test.want)
		}
	}
	t.Setenv("KC_TEST_BOOL", "")
	if got := envBool("KC_TEST_BOOL", true); !got {
		t.Fatal("empty environment value did not use fallback")
	}
}

func TestLoadIncludesExplicitSessionEncryptionKeyFallbacks(t *testing.T) {
	t.Setenv("SESSION_ENCRYPTION_KEY", "primary-key")
	t.Setenv("SESSION_ENCRYPTION_KEY_FALLBACKS", "legacy-a, primary-key, legacy-b, legacy-a")

	cfg := Load()
	keys := cfg.SessionEncryptionKeys()
	want := []string{"primary-key", "legacy-a", "legacy-b"}
	if len(keys) != len(want) {
		t.Fatalf("SessionEncryptionKeys() = %#v, want %#v", keys, want)
	}
	for index := range want {
		if keys[index] != want[index] {
			t.Fatalf("SessionEncryptionKeys()[%d] = %q, want %q", index, keys[index], want[index])
		}
	}
}
