// Package postgres provides the shared PostgreSQL driver implementation.
// Individual PostgreSQL-based backends (paradedb, postgresfts, pgtextsearch)
// import this package and register themselves separately.
package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/metrics"
)

// Driver implements the backends.Driver interface for PostgreSQL.
type Driver struct {
	pool       *pgxpool.Pool
	connString string
	extraGUCs  []string // Additional GUCs to capture (e.g., "paradedb.xxx")
}

// New creates a new PostgreSQL driver.
func New(connString string) (backends.Driver, error) {
	ctx := context.Background()

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	config.MaxConns = 20
	config.MinConns = 5
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	return &Driver{pool: pool, connString: connString}, nil
}

// Close closes the connection pool.
func (d *Driver) Close() error {
	if d.pool != nil {
		d.pool.Close()
	}
	return nil
}

// Pool returns the underlying connection pool for custom queries.
func (d *Driver) Pool() *pgxpool.Pool {
	return d.pool
}

// SetExtraGUCs sets additional GUCs to capture in CaptureConfig.
// GUCs are grouped by prefix (e.g., "paradedb.xxx" -> "paradedb" section).
func (d *Driver) SetExtraGUCs(gucs []string) {
	d.extraGUCs = gucs
}

// Exec executes SQL statements separated by semicolons.
func (d *Driver) Exec(ctx context.Context, statements string) error {
	for _, stmt := range strings.Split(statements, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		// Skip comment-only statements
		lines := strings.Split(stmt, "\n")
		var sqlLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
				sqlLines = append(sqlLines, line)
			}
		}
		stmt = strings.Join(sqlLines, "\n")
		if stmt == "" {
			continue
		}
		if _, err := d.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Query executes a search query and returns the hit count.
func (d *Driver) Query(ctx context.Context, query string, args ...any) (int, error) {
	rows, err := d.pool.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	return count, rows.Err()
}

// Insert bulk inserts rows using COPY.
func (d *Driver) Insert(ctx context.Context, table string, cols []string, rows [][]any) (int, error) {
	count, err := d.pool.CopyFrom(ctx,
		pgx.Identifier{table},
		cols,
		pgx.CopyFromRows(rows),
	)
	return int(count), err
}

// CaptureConfig captures database configuration and registers it with metrics.
func (d *Driver) CaptureConfig(ctx context.Context, backendName string) {
	config := make(map[string]interface{})

	// Base PostgreSQL settings
	baseSettings := []string{
		"shared_buffers", "work_mem", "effective_cache_size",
		"random_page_cost", "max_connections", "max_parallel_workers",
	}

	// Combine base + extra GUCs
	allSettings := append(baseSettings, d.extraGUCs...)

	rows, err := d.pool.Query(ctx, `
		SELECT name, setting, unit
		FROM pg_settings
		WHERE name = ANY($1)
	`, allSettings)
	if err == nil {
		defer rows.Close()
		pgSettings := make(map[string]string)
		extraByPrefix := make(map[string]map[string]string)

		for rows.Next() {
			var name, setting string
			var unit *string
			if rows.Scan(&name, &setting, &unit) == nil {
				value := setting
				if unit != nil && *unit != "" {
					value = setting + *unit
				}

				// Check if this is an extra GUC (has a dot prefix like "paradedb.xxx")
				if idx := strings.Index(name, "."); idx > 0 {
					prefix := name[:idx]
					if extraByPrefix[prefix] == nil {
						extraByPrefix[prefix] = make(map[string]string)
					}
					extraByPrefix[prefix][name] = value
				} else {
					pgSettings[name] = value
				}
			}
		}
		config["postgresql"] = pgSettings

		// Add extra GUC sections
		for prefix, settings := range extraByPrefix {
			config[prefix] = settings
		}
	}

	var version string
	if d.pool.QueryRow(ctx, "SELECT version()").Scan(&version) == nil {
		config["version"] = version
	}

	metrics.RegisterBackendConfig(backendName, config)
}
