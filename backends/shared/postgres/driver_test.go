package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPoolConfigKeepsOneVUConnectionAndCancelsInPlace(t *testing.T) {
	config, err := newPoolConfig("postgres://postgres:postgres@localhost:5432/benchmark")
	if err != nil {
		t.Fatalf("newPoolConfig: %v", err)
	}
	if config.MaxConns != 1 || config.MinConns != 1 {
		t.Fatalf("pool bounds = %d..%d, want exactly one connection", config.MinConns, config.MaxConns)
	}
	if config.MaxConnLifetime != benchmarkConnectionLifetime || config.MaxConnIdleTime != benchmarkConnectionLifetime {
		t.Fatalf(
			"pool connection lifetime/idle = %s/%s, want %s/%s",
			config.MaxConnLifetime,
			config.MaxConnIdleTime,
			benchmarkConnectionLifetime,
			benchmarkConnectionLifetime,
		)
	}

	conn := new(pgconn.PgConn)
	handler, ok := config.ConnConfig.BuildContextWatcherHandler(conn).(*pgconn.CancelRequestContextWatcherHandler)
	if !ok {
		t.Fatalf("context watcher = %T, want connection-preserving cancel request handler", handler)
	}
	if handler.Conn != conn {
		t.Fatal("cancel request handler does not target the VU connection")
	}
	if handler.CancelRequestDelay != 0 || handler.DeadlineDelay != queryCancelDeadlineDelay {
		t.Fatalf(
			"cancel request delay/deadline = %s/%s, want 0/%s",
			handler.CancelRequestDelay,
			handler.DeadlineDelay,
			queryCancelDeadlineDelay,
		)
	}
}

func TestCanceledQueryKeepsBackendPID(t *testing.T) {
	connString := os.Getenv("BENCHMARKER_POSTGRES_TEST_URL")
	if connString == "" {
		t.Skip("BENCHMARKER_POSTGRES_TEST_URL is not set")
	}

	driverValue, err := New(connString)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	driver := driverValue.(*Driver)
	t.Cleanup(func() { _ = driver.Close() })

	var before int
	if err := driver.pool.QueryRow(context.Background(), "SELECT pg_backend_pid()").Scan(&before); err != nil {
		t.Fatalf("query backend PID before cancellation: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := driver.Query(ctx, "SELECT pg_sleep(10)"); err == nil {
		t.Fatal("long query was not canceled")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
			t.Fatalf("canceled query error = %T %v, want PostgreSQL 57014", err, err)
		}
	}

	var after int
	if err := driver.pool.QueryRow(context.Background(), "SELECT pg_backend_pid()").Scan(&after); err != nil {
		t.Fatalf("query backend PID after cancellation: %v", err)
	}
	if after != before {
		t.Fatalf("backend PID changed across cancellation: %d -> %d", before, after)
	}
}
