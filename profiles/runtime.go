package profiles

import (
	"fmt"
	"sort"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"gopkg.in/yaml.v3"
)

// LegacyType identifies selectors whose k6 environment and alias defaults
// predate profiles. ParadeDB used to register a separate PostgreSQL factory.
func LegacyType(name string) bool {
	_, registered := backends.GetConfig(name)
	return registered || name == "paradedb"
}

// DefaultNames preserves the loader's historical all-backends selection without
// registering PostgreSQL extension profiles as database drivers.
func DefaultNames() []string {
	names := backends.RegisteredBackends()
	if _, registered := backends.GetConfig("paradedb"); !registered {
		names = append(names, "paradedb")
	}
	sort.Strings(names)
	return names
}

func fallbackManifest(name string) ([]byte, error) {
	cfg, ok := backends.GetConfig(name)
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	return yaml.Marshal(Manifest{Backend: name, Container: &cfg.Container, Connection: cfg.DefaultConn, EnvVar: &cfg.EnvVar})
}

func (w Profile) BackendConfig() (backends.BackendConfig, error) {
	cfg, ok := backends.GetConfig(w.Backend)
	if !ok {
		return cfg, fmt.Errorf("profile %q: unknown backend %q", w.Name, w.Backend)
	}
	e := w.Extras
	if w.Backend != "postgres" && (len(e.GUCs) > 0 || len(e.GUCPrefixes) > 0 || len(e.Queries) > 0 || e.IndexAccessMethods != nil) {
		return cfg, fmt.Errorf("profile %q: extras require the postgres backend", w.Name)
	}
	return cfg, nil
}

func (w Profile) NewDriver(connection string) (backends.Driver, error) {
	cfg, err := w.BackendConfig()
	if err != nil {
		return nil, err
	}
	driver, err := cfg.Factory(connection)
	if err != nil {
		return nil, err
	}
	w.ConfigureDriver(driver)
	return driver, nil
}

// ConfigureDriver applies PostgreSQL profile capture and telemetry settings.
func (w Profile) ConfigureDriver(driver backends.Driver) {
	if pg, ok := driver.(*postgres.Driver); ok {
		if w.Extras.GUCs != nil {
			pg.SetExtraGUCs(w.Extras.GUCs)
		}
		if w.Extras.GUCPrefixes != nil {
			pg.SetExtraGUCPrefixes(w.Extras.GUCPrefixes)
		}
		queries := make([]postgres.ConfigQuery, 0, len(w.Extras.Queries))
		for _, q := range w.Extras.Queries {
			queries = append(queries, postgres.ConfigQuery{Key: q.Key, Query: q.SQL})
		}
		if w.Extras.Queries != nil {
			pg.SetExtraQueriesWithColumnNames(queries)
		}
		if w.Extras.IndexAccessMethods != nil {
			queries := postgres.StandardTelemetryQueries()
			queries = append(queries, postgres.IndexIOTelemetryQueries(w.Extras.IndexAccessMethods...)...)
			pg.SetTelemetryQueries(queries...)
		}
	}
}

func (w Profile) CLILoader(connection string) (*backends.CLILoader, error) {
	cfg, err := w.BackendConfig()
	if err != nil {
		return nil, err
	}
	return backends.NewProfileCLILoader(w.Name, w.Backend, cfg.FileType, connection, w.NewDriver), nil
}
