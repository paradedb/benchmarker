// Package postgres provides the shared PostgreSQL driver implementation.
// Individual PostgreSQL-based backends (paradedb, postgresfts)
// import this package and register themselves separately.
package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nickbruun/pgsplit"
	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/metrics"
)

// ConfigQuery is a custom SQL query whose scalar result is captured during CaptureConfig.
// The query must return a single row with a single text-compatible column.
type ConfigQuery struct {
	Key   string // Config map key (e.g., "paradedb_version")
	Query string // SQL to execute (e.g., "SELECT paradedb.version_info()")
}

// Driver implements the backends.Driver interface for PostgreSQL.
type Driver struct {
	pool         *pgxpool.Pool
	connString   string
	extraGUCs    []string      // Additional GUCs to capture (e.g., "paradedb.xxx")
	extraQueries []ConfigQuery // Additional SQL queries to capture
}

// New creates a new PostgreSQL driver.
func New(connString string) (backends.Driver, error) {
	ctx := context.Background()

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	config.MaxConns = 1
	config.MinConns = 1
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

// SetExtraQueries sets additional SQL queries to run during CaptureConfig.
// Each query should return a single scalar text value.
func (d *Driver) SetExtraQueries(queries []ConfigQuery) {
	d.extraQueries = queries
}

// Exec executes SQL statements separated by semicolons.
func (d *Driver) Exec(ctx context.Context, statements string) error {
	stmts, err := pgsplit.SplitStatements(statements)
	if err != nil {
		return err
	}
	for _, stmt := range stmts {
		if _, err := d.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Query executes a query and returns the hit count.
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

// Update upserts rows using INSERT ... ON CONFLICT DO UPDATE.
// keyCols are the conflict target columns, cols is all columns (keys first, then values).
func (d *Driver) Update(ctx context.Context, table string, keyCols []string, cols []string, rows [][]any) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	// Build value columns (everything not in keyCols)
	keySet := make(map[string]bool, len(keyCols))
	for _, k := range keyCols {
		keySet[k] = true
	}
	var valCols []string
	for _, c := range cols {
		if !keySet[c] {
			valCols = append(valCols, c)
		}
	}

	// Build: INSERT INTO t (cols) VALUES ($1,$2,...), ($3,$4,...) ON CONFLICT (keyCols) DO UPDATE SET col=EXCLUDED.col, ...
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(table)
	b.WriteString(" (")
	b.WriteString(strings.Join(cols, ", "))
	b.WriteString(") VALUES ")

	paramIdx := 1
	args := make([]any, 0, len(rows)*len(cols))
	for i, row := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for j := range cols {
			if j > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("$%d", paramIdx))
			paramIdx++
			args = append(args, row[j])
		}
		b.WriteByte(')')
	}

	b.WriteString(" ON CONFLICT (")
	b.WriteString(strings.Join(keyCols, ", "))
	b.WriteString(") DO UPDATE SET ")
	for i, col := range valCols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(col)
		b.WriteString(" = EXCLUDED.")
		b.WriteString(col)
	}

	tag, err := d.pool.Exec(ctx, b.String(), args...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// CaptureConfig captures database configuration and registers it with metrics.
func (d *Driver) CaptureConfig(ctx context.Context, backendName string) {
	config := make(map[string]interface{})

	// Base PostgreSQL settings
	baseSettings := []string{
		"shared_buffers", "work_mem", "effective_cache_size",
		"random_page_cost", "max_connections", "max_parallel_workers",
		"max_parallel_workers_per_gather", "jit",
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

	// Run config queries (base + specialization-registered)
	allQueries := append([]ConfigQuery{
		{Key: "version", Query: "SELECT version()"},
	}, d.extraQueries...)
	for _, q := range allQueries {
		var result string
		if d.pool.QueryRow(ctx, q.Query).Scan(&result) == nil {
			config[q.Key] = result
		}
	}

	metrics.RegisterBackendConfig(backendName, config)
}
