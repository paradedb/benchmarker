package backends

import (
	"fmt"

	"github.com/paradedb/benchmarker/drivers"
	"github.com/paradedb/benchmarker/drivers/shared/postgres"
)

// DefaultNames preserves the loader's historical default targets. Each name
// must resolve to a backend manifest; registering a driver never adds a target.
func DefaultNames() []string {
	return []string{"clickhouse", "elasticsearch", "mongodb", "opensearch", "paradedb", "postgres", "turbopuffer"}
}

func (w Backend) DriverConfig() (drivers.DriverConfig, error) {
	cfg, ok := drivers.GetConfig(w.DriverName)
	if !ok {
		return cfg, fmt.Errorf("backend %q: unknown driver %q", w.Name, w.DriverName)
	}
	e := w.Extras
	if w.DriverName != "postgres" && (len(e.GUCs) > 0 || len(e.GUCPrefixes) > 0 || len(e.Queries) > 0 || e.IndexAccessMethods != nil) {
		return cfg, fmt.Errorf("backend %q: extras require the postgres driver", w.Name)
	}
	return cfg, nil
}

func (w Backend) NewDriver(connection string) (drivers.Driver, error) {
	cfg, err := w.DriverConfig()
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

// ConfigureDriver applies PostgreSQL backend capture and telemetry settings.
func (w Backend) ConfigureDriver(driver drivers.Driver) {
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

func (w Backend) CLILoader(connection string) (*drivers.CLILoader, error) {
	cfg, err := w.DriverConfig()
	if err != nil {
		return nil, err
	}
	return drivers.NewBackendCLILoader(w.Name, w.DriverName, cfg.FileType, connection, w.NewDriver), nil
}
