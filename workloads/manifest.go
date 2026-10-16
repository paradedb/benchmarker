// Package workloads resolves declarative index/query strategies independently
// of their database drivers. Built-ins are embedded so binaries are portable.
package workloads

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var builtins embed.FS

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Query accepts either SQL (the result column supplies its key), or a mapping
// with an explicit key for compatibility with existing config captures.
type Query struct {
	Key string `yaml:"key"`
	SQL string `yaml:"query"`
}

func (q *Query) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		q.SQL = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("extras.queries entries must be SQL strings or key/query mappings")
	}
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k != "key" && k != "query" {
			return fmt.Errorf("unknown query field %q", k)
		}
	}
	type plain Query
	return n.Decode((*plain)(q))
}

type Extras struct {
	GUCs        []string `yaml:"gucs"`
	GUCPrefixes []string `yaml:"gucPrefixes"`
	Queries     []Query  `yaml:"queries"`
	// When present, replace the driver's index-I/O access methods while keeping
	// its standard PostgreSQL telemetry. An empty list disables index I/O only.
	IndexAccessMethods []string `yaml:"indexAccessMethods"`
}

type Manifest struct {
	Backend     string  `yaml:"backend"`
	Container   *string `yaml:"container"`
	Connection  string  `yaml:"connection"`
	EnvVar      *string `yaml:"envVar"`
	Description string  `yaml:"description"`
	Color       string  `yaml:"color"`
	Extras      Extras  `yaml:"extras,omitempty"`
}

type Workload struct {
	Manifest
	Name string
}

func (w Workload) ContainerName() string {
	if w.Container != nil {
		return *w.Container
	}
	return w.Name
}

func (w Workload) EnvironmentVariable() string {
	if w.EnvVar != nil {
		return *w.EnvVar
	}
	return strings.ToUpper(strings.ReplaceAll(w.Name, "-", "_")) + "_URL"
}

// ResolveConnection gives explicit configuration precedence over environment
// and manifest defaults. Legacy k6 calls do not call this method.
func (w Workload) ResolveConnection(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if value := os.Getenv(w.EnvironmentVariable()); value != "" {
		return value, nil
	}
	if w.Connection != "" {
		return w.Connection, nil
	}
	return "", fmt.Errorf("workload %q requires a connection (set connection or %s)", w.Name, w.EnvironmentVariable())
}

func decode(data []byte) (Manifest, error) {
	var m Manifest
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("expected one YAML document")
	}
	return m, nil
}

func validate(name string, m Manifest) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid workload name %q", name)
	}
	if !validName.MatchString(m.Backend) {
		return fmt.Errorf("workload %q requires a valid backend", name)
	}
	for _, q := range m.Extras.Queries {
		if strings.TrimSpace(q.SQL) == "" {
			return fmt.Errorf("workload %q has an empty config query", name)
		}
	}
	return nil
}

// Builtin reads only the embedded manifest. Compatibility factories deliberately
// ignore filesystem overrides so old APIs retain their original behavior.
func Builtin(name string) (Workload, error) {
	data, err := builtins.ReadFile(name + ".yaml")
	if err != nil {
		return Workload{}, err
	}
	m, err := decode(data)
	if err == nil {
		err = validate(name, m)
	}
	return Workload{Manifest: m, Name: name}, err
}

// Resolve uses an external global manifest (if present), otherwise an embedded
// manifest or registered-backend fallback. The optional dataset manifest replaces
// whole top-level fields, including extras; it does not merge nested lists.
// dir defaults to ./workloads; an explicitly supplied directory must exist.
func Resolve(name, dataset, dir string) (Workload, error) {
	if !validName.MatchString(name) {
		return Workload{}, fmt.Errorf("invalid workload name %q", name)
	}
	if dir == "" {
		dir = "workloads"
	} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Workload{}, fmt.Errorf("workloads directory %q does not exist or is not a directory", dir)
	}
	// #nosec G304 -- Manifest directory is explicitly selected by the local CLI/script; name is validated.
	data, err := os.ReadFile(filepath.Join(dir, name+".yaml"))
	if os.IsNotExist(err) {
		data, err = builtins.ReadFile(name + ".yaml")
		if os.IsNotExist(err) {
			data, err = fallbackManifest(name)
		}
	}
	if err != nil {
		return Workload{}, fmt.Errorf("workload %q: %w", name, err)
	}
	// Validate even fields subsequently overridden, to catch typos at their source.
	baseManifest, err := decode(data)
	if err == nil {
		err = validate(name, baseManifest)
	}
	if err != nil {
		return Workload{}, fmt.Errorf("workload %q: %w", name, err)
	}
	if dataset != "" {
		path := filepath.Join(dataset, name, "workload.yaml")
		// #nosec G304 -- Dataset directory is explicitly selected by the local CLI/script; name is validated.
		override, err := os.ReadFile(path)
		if err == nil {
			if _, err := decode(override); err != nil {
				return Workload{}, fmt.Errorf("%s: %w", path, err)
			}
			var base, patch map[string]yaml.Node
			if err := yaml.Unmarshal(data, &base); err != nil {
				return Workload{}, err
			}
			if err := yaml.Unmarshal(override, &patch); err != nil {
				return Workload{}, err
			}
			for k, v := range patch {
				base[k] = v
			}
			data, err = yaml.Marshal(base)
			if err != nil {
				return Workload{}, err
			}
		} else if !os.IsNotExist(err) {
			return Workload{}, err
		}
	}
	m, err := decode(data)
	if err == nil {
		err = validate(name, m)
	}
	if err != nil {
		return Workload{}, fmt.Errorf("workload %q: %w", name, err)
	}
	return Workload{Manifest: m, Name: name}, nil
}
