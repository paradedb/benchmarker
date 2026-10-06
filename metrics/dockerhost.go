package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const defaultDockerSocket = "/var/run/docker.sock"

// dockerEndpoint resolves the Docker daemon address the same way the docker
// CLI does: DOCKER_HOST, then the DOCKER_CONTEXT / currentContext endpoint,
// then the default socket. Colima, OrbStack, and Docker Desktop all expose
// their daemon through a context rather than /var/run/docker.sock.
func dockerEndpoint() (network, address string) {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		if network, address, ok := parseDockerHost(host); ok {
			return network, address
		}
	}
	if host := contextDockerHost(dockerConfigDir()); host != "" {
		if network, address, ok := parseDockerHost(host); ok {
			return network, address
		}
	}
	return "unix", defaultDockerSocket
}

func parseDockerHost(host string) (network, address string, ok bool) {
	switch {
	case strings.HasPrefix(host, "unix://"):
		return "unix", strings.TrimPrefix(host, "unix://"), true
	case strings.HasPrefix(host, "tcp://"):
		return "tcp", strings.TrimPrefix(host, "tcp://"), true
	default:
		return "", "", false
	}
}

func dockerConfigDir() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker")
}

// contextDockerHost returns the docker endpoint of the active context, or ""
// when the default context is active or the context can't be read.
func contextDockerHost(configDir string) string {
	if configDir == "" {
		return ""
	}

	name := os.Getenv("DOCKER_CONTEXT")
	if name == "" {
		// #nosec G304 -- Reads the local user's Docker CLI config, as the docker CLI does.
		data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
		if err != nil {
			return ""
		}
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if json.Unmarshal(data, &cfg) != nil {
			return ""
		}
		name = cfg.CurrentContext
	}
	if name == "" || name == "default" {
		return ""
	}

	// Context metadata lives under a directory named by the SHA-256 of the
	// context name.
	digest := sha256.Sum256([]byte(name))
	metaPath := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(digest[:]), "meta.json")
	// #nosec G304 -- Reads the local user's Docker CLI context metadata, as the docker CLI does.
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return meta.Endpoints.Docker.Host
}
