package opensearch

import "testing"

func TestDriverConfigDefaultsToVerifiedTLS(t *testing.T) {
	t.Setenv(skipTLSVerifyEnv, "")

	config := driverConfig()
	if config.SkipTLSVerify {
		t.Fatal("expected TLS verification to remain enabled by default")
	}
}

func TestDriverConfigAllowsOptInTLSBypass(t *testing.T) {
	t.Setenv(skipTLSVerifyEnv, "true")

	config := driverConfig()
	if !config.SkipTLSVerify {
		t.Fatal("expected TLS verification bypass to be enabled when requested")
	}
}

func TestDriverConfigIgnoresInvalidTLSBypassValues(t *testing.T) {
	t.Setenv(skipTLSVerifyEnv, "definitely-not-bool")

	config := driverConfig()
	if config.SkipTLSVerify {
		t.Fatal("expected invalid TLS bypass values to fall back to secure default")
	}
}
