package vectorsearch

import (
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
)

func TestRegistrationAndVectorSchema(t *testing.T) {
	cfg, ok := backends.GetConfig("vectorsearch")
	if !ok {
		t.Fatal("vectorsearch is not registered")
	}
	if got := backends.ConnectionEnvVars()["vectorsearch"]; got != "VECTORSEARCH_URL" {
		t.Fatalf("connection environment variable = %q", got)
	}
	if cfg.FileType != "sql" || cfg.Container != "vectorsearch" {
		t.Fatalf("unexpected backend config: %+v", cfg)
	}
	loader := backends.GetCLILoader("vectorsearch", cfg.DefaultConn)
	if err := loader.ValidateSchema(&backends.Schema{
		Table: "items", Columns: map[string]string{"id": "bigint", "emb": "vector(3)"},
	}); err != nil {
		t.Fatalf("vector schema rejected: %v", err)
	}
	driver, err := cfg.Factory(cfg.DefaultConn)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	pgDriver, ok := driver.(*postgres.Driver)
	if !ok || !pgDriver.TelemetryEnabled() {
		t.Fatal("expected shared PostgreSQL driver with telemetry")
	}
}
