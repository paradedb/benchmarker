// Package clickhouse provides the ClickHouse driver implementation.
package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
)

func init() {
	backends.Register("clickhouse", backends.BackendConfig{
		Factory:     New,
		FileType:    "sql",
		EnvVar:      "CLICKHOUSE_URL",
		DefaultConn: "clickhouse://default:clickhouse@localhost:9000/default",
		Container:   "clickhouse",
	})
}

// Driver implements the backends.Driver interface for ClickHouse.
type Driver struct {
	conn driver.Conn
}

// New creates a new ClickHouse driver.
func New(connString string) (backends.Driver, error) {
	opts, err := clickhouse.ParseDSN(connString)
	if err != nil {
		return nil, err
	}

	opts.MaxOpenConns = 20
	opts.MaxIdleConns = 5
	opts.ConnMaxLifetime = 30 * time.Minute

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	if err := conn.Ping(ctx); err != nil {
		conn.Close()
		return nil, err
	}

	return &Driver{conn: conn}, nil
}

// Close closes the connection.
func (d *Driver) Close() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}

// Exec executes SQL statements separated by semicolons.
func (d *Driver) Exec(ctx context.Context, statements string) error {
	for _, stmt := range strings.Split(statements, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := d.conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("executing %q: %w", truncate(stmt, 50), err)
		}
	}
	return nil
}

// Query executes a search query and returns the hit count.
func (d *Driver) Query(ctx context.Context, query string, args ...any) (int, error) {
	rows, err := d.conn.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	colTypes := rows.ColumnTypes()
	values := make([]interface{}, len(colTypes))
	for i := range values {
		values[i] = new(interface{})
	}

	for rows.Next() {
		if rows.Scan(values...) == nil {
			count++
		}
	}
	return count, rows.Err()
}

// Insert bulk inserts rows using batch API.
func (d *Driver) Insert(ctx context.Context, table string, cols []string, rows [][]any) (int, error) {
	query := fmt.Sprintf("INSERT INTO %s (%s)", table, strings.Join(cols, ", "))

	batch, err := d.conn.PrepareBatch(ctx, query)
	if err != nil {
		return 0, err
	}

	for _, row := range rows {
		if err := batch.Append(row...); err != nil {
			return 0, err
		}
	}

	if err := batch.Send(); err != nil {
		return 0, err
	}

	return len(rows), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// CaptureConfig captures database configuration and registers it with metrics.
func (d *Driver) CaptureConfig(ctx context.Context, backendName string) {
	config := make(map[string]interface{})

	var version string
	if d.conn.QueryRow(ctx, "SELECT version()").Scan(&version) == nil {
		config["version"] = version
	}

	metrics.RegisterBackendConfig(backendName, config)
}
