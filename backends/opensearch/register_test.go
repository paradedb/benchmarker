package opensearch

import "testing"

func TestTLSBypassRejected(t *testing.T) {
	t.Setenv(skipTLSVerifyEnv, "true")
	if _, err := New("https://localhost:9201"); err == nil {
		t.Fatal("expected TLS bypass to be rejected")
	}
}

func TestVerifiedTLSDefault(t *testing.T) {
	t.Setenv(skipTLSVerifyEnv, "")
	if _, err := New("https://localhost:9201"); err != nil {
		t.Fatal(err)
	}
}
