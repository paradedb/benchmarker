package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeDockerContext(t *testing.T, configDir, name, host string) {
	t.Helper()
	digest := sha256.Sum256([]byte(name))
	dir := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDockerConfig(t *testing.T, configDir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDockerEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		dockerHost    string
		dockerContext string
		config        string
		wantNetwork   string
		wantAddress   string
	}{
		{
			name:        "no config falls back to default socket",
			wantNetwork: "unix",
			wantAddress: defaultDockerSocket,
		},
		{
			name:        "DOCKER_HOST unix socket",
			dockerHost:  "unix:///tmp/custom.sock",
			config:      `{"currentContext":"colima"}`,
			wantNetwork: "unix",
			wantAddress: "/tmp/custom.sock",
		},
		{
			name:        "DOCKER_HOST tcp",
			dockerHost:  "tcp://127.0.0.1:2375",
			wantNetwork: "tcp",
			wantAddress: "127.0.0.1:2375",
		},
		{
			name:        "currentContext from config.json",
			config:      `{"currentContext":"colima"}`,
			wantNetwork: "unix",
			wantAddress: "/home/me/.colima/default/docker.sock",
		},
		{
			name:          "DOCKER_CONTEXT overrides config.json",
			dockerContext: "orbstack",
			config:        `{"currentContext":"colima"}`,
			wantNetwork:   "unix",
			wantAddress:   "/home/me/.orbstack/run/docker.sock",
		},
		{
			name:        "default context uses default socket",
			config:      `{"currentContext":"default"}`,
			wantNetwork: "unix",
			wantAddress: defaultDockerSocket,
		},
		{
			name:        "unknown context falls back to default socket",
			config:      `{"currentContext":"missing"}`,
			wantNetwork: "unix",
			wantAddress: defaultDockerSocket,
		},
		{
			name:        "unsupported DOCKER_HOST scheme falls through to context",
			dockerHost:  "ssh://user@remote",
			config:      `{"currentContext":"colima"}`,
			wantNetwork: "unix",
			wantAddress: "/home/me/.colima/default/docker.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			writeDockerContext(t, configDir, "colima", "unix:///home/me/.colima/default/docker.sock")
			writeDockerContext(t, configDir, "orbstack", "unix:///home/me/.orbstack/run/docker.sock")
			if tt.config != "" {
				writeDockerConfig(t, configDir, tt.config)
			}
			t.Setenv("DOCKER_CONFIG", configDir)
			t.Setenv("DOCKER_HOST", tt.dockerHost)
			t.Setenv("DOCKER_CONTEXT", tt.dockerContext)

			network, address := dockerEndpoint()
			if network != tt.wantNetwork || address != tt.wantAddress {
				t.Fatalf("dockerEndpoint() = %s %s, want %s %s", network, address, tt.wantNetwork, tt.wantAddress)
			}
		})
	}
}
