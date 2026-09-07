// Package postgres provides the shared PostgreSQL driver implementation.
// Individual PostgreSQL-based backends (paradedb, postgres)
// import this package and register themselves separately.
package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	"github.com/nickbruun/pgsplit"
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

// ConfigQuery is a custom SQL query whose result is captured during CaptureConfig.
// A query returning a single text column stores its first row under Key. A query
// returning two text columns is treated as (name, value) rows; each name is
// grouped into a config section by its dot-prefix, the same way GUCs are
// (e.g., "paradedb.segments_at_run_start.idx" lands in the "paradedb" section).
type ConfigQuery struct {
	Key   string // Config map key for single-column results (e.g., "paradedb_version")
	Query string // SQL to execute (e.g., "SELECT paradedb.version_info()::text")
}

// Driver implements the backends.Driver interface for PostgreSQL.
type Driver struct {
	pool             *pgxpool.Pool
	connString       string
	extraGUCs        []string      // Additional GUCs to capture (e.g., "paradedb.xxx")
	extraGUCPrefixes []string      // GUC prefixes captured wholesale (e.g., "paradedb")
	extraQueries     []ConfigQuery // Additional SQL queries to capture
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

	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// Best-effort: fails harmlessly when the vector extension is not installed.
		_ = pgxvector.RegisterTypes(ctx, conn)
		return nil
	}

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

// ReloadTypes re-establishes pooled connections so AfterConnect type
// registration sees extensions created after the pool first connected.
func (d *Driver) ReloadTypes(ctx context.Context) error {
	d.pool.Reset()
	return nil
}

// SetExtraGUCs sets additional GUCs to capture in CaptureConfig.
// GUCs are grouped by prefix (e.g., "paradedb.xxx" -> "paradedb" section).
func (d *Driver) SetExtraGUCs(gucs []string) {
	d.extraGUCs = gucs
}

// SetExtraGUCPrefixes sets GUC prefixes to capture wholesale in CaptureConfig.
// Every pg_settings entry under "<prefix>." is captured into a section named
// after the prefix (e.g., "paradedb" -> all "paradedb.*" GUCs).
func (d *Driver) SetExtraGUCPrefixes(prefixes []string) {
	d.extraGUCPrefixes = prefixes
}

// SetExtraQueries sets additional SQL queries to run during CaptureConfig.
// See ConfigQuery for the supported result shapes.
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

// wrapVectors converts neutral []float32 vector values (as produced by the
// shared row-source layer) into pgvector.Vector in place, which the registered
// pgx codec binary-encodes for vector(n) columns.
func wrapVectors(rows [][]any) {
	for _, row := range rows {
		for i, value := range row {
			if v, ok := value.([]float32); ok {
				row[i] = pgvector.NewVector(v)
			}
		}
	}
}

// Insert bulk inserts rows using COPY.
func (d *Driver) Insert(ctx context.Context, table string, cols []string, rows [][]any) (int, error) {
	wrapVectors(rows)
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
	wrapVectors(rows)

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

	likePatterns := make([]string, len(d.extraGUCPrefixes))
	for i, prefix := range d.extraGUCPrefixes {
		likePatterns[i] = prefix + ".%"
	}

	pgSettings := make(map[string]string)
	extraByPrefix := make(map[string]map[string]string)

	// addEntry groups a name/value pair by dot-prefix (e.g. "paradedb.xxx"
	// lands in the "paradedb" section, unprefixed names in "postgresql").
	addEntry := func(name, value string) {
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

	rows, err := d.pool.Query(ctx, `
		SELECT name, setting, unit
		FROM pg_settings
		WHERE name = ANY($1) OR name LIKE ANY($2)
	`, allSettings, likePatterns)
	if err == nil {
		for rows.Next() {
			var name, setting string
			var unit *string
			if rows.Scan(&name, &setting, &unit) == nil {
				value := setting
				if unit != nil && *unit != "" {
					value = setting + *unit
				}
				addEntry(name, value)
			}
		}
		rows.Close()
	}

	// Run config queries (base + specialization-registered)
	allQueries := append([]ConfigQuery{
		{Key: "version", Query: "SELECT version()"},
	}, d.extraQueries...)
	for _, q := range allQueries {
		rows, err := d.pool.Query(ctx, q.Query)
		if err != nil {
			continue
		}
		twoColumns := len(rows.FieldDescriptions()) >= 2
		for rows.Next() {
			if twoColumns {
				var name, value string
				if rows.Scan(&name, &value) == nil {
					addEntry(name, value)
				}
			} else {
				var result string
				if rows.Scan(&result) == nil {
					config[q.Key] = result
				}
			}
		}
		rows.Close()
	}

	config["postgresql"] = pgSettings
	for prefix, settings := range extraByPrefix {
		config[prefix] = settings
	}

	metrics.RegisterBackendConfig(backendName, config)
}
