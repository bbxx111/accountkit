package main

const policySeedMarker = "SDK_POLICY_SEED_USER_ID="

// readinessMetadata describes only the private integration-test control protocol.
func readinessMetadata(origin, controlOrigin, apiPrefix, adminPrefix, profile, mode string) map[string]string {
	return map[string]string{
		"origin": origin, "control_origin": controlOrigin,
		"api_prefix": apiPrefix, "admin_prefix": adminPrefix, "profile": profile,
		"baseline": serviceBaseline, "mode": mode, "policy_seed_marker": policySeedMarker,
	}
}
