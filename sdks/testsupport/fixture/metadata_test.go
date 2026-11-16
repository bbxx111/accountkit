package main

import "testing"

func TestFixtureReadinessMetadata(t *testing.T) {
	got := readinessMetadata("https://service.test", "https://control.test", "/custom/account", "/custom/admin", "custom-extended", "embedded-test-host")
	want := map[string]string{
		"origin": "https://service.test", "control_origin": "https://control.test",
		"api_prefix": "/custom/account", "admin_prefix": "/custom/admin", "profile": "custom-extended",
		"baseline": "v1.0.0", "mode": "embedded-test-host", "policy_seed_marker": "SDK_POLICY_SEED_USER_ID=",
	}
	if len(got) != len(want) {
		t.Fatalf("metadata fields = %v", got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q want %q", key, got[key], value)
		}
	}
}
