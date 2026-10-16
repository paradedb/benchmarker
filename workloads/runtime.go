package workloads

import (
	"fmt"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"gopkg.in/yaml.v3"
)

func fallbackManifest(name string) ([]byte, error) {
	cfg, ok := backends.GetConfig(name)
	if !ok {
		return nil, fmt.Errorf("unknown workload %q", name)
	}
	return yaml.Marshal(Manifest{Backend: name, Container: &cfg.Container, Connection: cfg.DefaultConn, EnvVar: &cfg.EnvVar})
}

// CheckBackend rejects conflicting engine/workload combinations. Legacy names
// such as paradedb are accepted when their embedded workload uses this engine.
func (w Workload) CheckBackend(name string) error {
	if name == "" || name == w.Backend {
		return nil
	}
	if legacy, err := Builtin(name); err == nil && legacy.Backend == w.Backend {
		return nil
	}
	return fmt.Errorf("workload %q uses backend %q, not %q", w.Name, w.Backend, name)
}

func (w Workload) BackendConfig() (backends.BackendConfig, error) {
	cfg, ok := backends.GetConfig(w.Backend)
	if !ok {
		return cfg, fmt.Errorf("workload %q: unknown backend %q", w.Name, w.Backend)
	}
	e := w.Extras
	if w.Backend != "postgres" && (len(e.GUCs) > 0 || len(e.GUCPrefixes) > 0 || len(e.Queries) > 0 || e.IndexAccessMethods != nil) {
		return cfg, fmt.Errorf("workload %q: extras require the postgres backend", w.Name)
	}
	return cfg, nil
}

func (w Workload) NewDriver(connection string) (backends.Driver, error) {
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

// ConfigureDriver applies PostgreSQL workload capture and telemetry settings.
// It also lets legacy constructors retain their original factory and registration
// side effects while sharing the declarative specialization.
func (w Workload) ConfigureDriver(driver backends.Driver) {
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

func (w Workload) CLILoader(connection string) (*backends.CLILoader, error) {
	cfg, err := w.BackendConfig()
	if err != nil {
		return nil, err
	}
	return backends.NewWorkloadCLILoader(w.Name, w.Backend, cfg.FileType, connection, w.NewDriver), nil
}
