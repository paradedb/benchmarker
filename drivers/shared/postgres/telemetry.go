package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/paradedb/benchmarker/metrics"
)

// TelemetryQuery is the repeating counterpart to ConfigQuery. Its SQL returns
// (series text, value double precision, unit text, labels jsonb) rows.
type TelemetryQuery struct {
	Name  string
	Kind  metrics.TelemetryKind
	Query string
	Args  []any
}

func (d *Driver) SetTelemetryQueries(queries ...TelemetryQuery) {
	d.telemetryQueries = append([]TelemetryQuery(nil), queries...)
}

func (d *Driver) AddTelemetryQueries(queries ...TelemetryQuery) {
	d.telemetryQueries = append(d.telemetryQueries, queries...)
}

func (d *Driver) TelemetryEnabled() bool {
	return len(d.telemetryQueries) > 0
}

// ReadTelemetry executes every configured query. A failed optional PostgreSQL
// view does not discard series returned by the other queries.
func (d *Driver) ReadTelemetry(ctx context.Context) ([]metrics.TelemetryPoint, error) {
	points := make([]metrics.TelemetryPoint, 0)
	var queryErrors []error
	for _, query := range d.telemetryQueries {
		rows, err := d.pool.Query(ctx, query.Query, query.Args...)
		if err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("%s: %w", query.Name, err))
			continue
		}
		for rows.Next() {
			var series, unit string
			var value float64
			var labelsJSON []byte
			if err := rows.Scan(&series, &value, &unit, &labelsJSON); err != nil {
				queryErrors = append(queryErrors, fmt.Errorf("%s: %w", query.Name, err))
				break
			}
			labels := make(map[string]string)
			if len(labelsJSON) > 0 {
				if err := json.Unmarshal(labelsJSON, &labels); err != nil {
					queryErrors = append(queryErrors, fmt.Errorf("%s labels: %w", query.Name, err))
					continue
				}
			}
			points = append(points, metrics.TelemetryPoint{
				Name:   strings.Trim(query.Name+"."+series, "."),
				Kind:   query.Kind,
				Unit:   unit,
				Value:  value,
				Labels: labels,
			})
		}
		if err := rows.Err(); err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("%s: %w", query.Name, err))
		}
		rows.Close()
	}
	return points, errors.Join(queryErrors...)
}

// StandardTelemetryQueries returns PostgreSQL WAL, database, writer, I/O, and
// activity series. Version-specific views are separate so unsupported views
// are reported without suppressing the remaining telemetry.
func StandardTelemetryQueries() []TelemetryQuery {
	return []TelemetryQuery{
		{
			Name: "postgres.wal", Kind: metrics.TelemetryCounter,
			Query: `
				SELECT series, value, unit, '{}'::jsonb
				FROM pg_stat_wal AS wal
				CROSS JOIN LATERAL (VALUES
					('records', wal.wal_records::double precision, 'count'),
					('full_pages', wal.wal_fpi::double precision, 'count'),
					('bytes', wal.wal_bytes::double precision, 'bytes'),
					('buffers_full', wal.wal_buffers_full::double precision, 'count')
				) AS metric_values(series, value, unit)`,
		},
		{
			Name: "postgres.database", Kind: metrics.TelemetryCounter,
			Query: `
				SELECT series, value, unit, '{}'::jsonb
				FROM pg_stat_database AS database
				CROSS JOIN LATERAL (VALUES
					('blocks_read', database.blks_read::double precision, 'blocks'),
					('blocks_hit', database.blks_hit::double precision, 'blocks'),
					('block_read_time', database.blk_read_time::double precision, 'ms'),
					('block_write_time', database.blk_write_time::double precision, 'ms'),
					('temp_files', database.temp_files::double precision, 'count'),
					('temp_bytes', database.temp_bytes::double precision, 'bytes'),
					('deadlocks', database.deadlocks::double precision, 'count')
				) AS metric_values(series, value, unit)
				WHERE database.datname = current_database()`,
		},
		{
			Name: "postgres.background_writer", Kind: metrics.TelemetryCounter,
			Query: `
				SELECT series, value, unit, '{}'::jsonb
				FROM pg_stat_bgwriter AS writer
				CROSS JOIN LATERAL (VALUES
					('buffers_clean', writer.buffers_clean::double precision, 'blocks'),
					('maxwritten_clean', writer.maxwritten_clean::double precision, 'count'),
					('buffers_allocated', writer.buffers_alloc::double precision, 'blocks')
				) AS metric_values(series, value, unit)`,
		},
		{
			Name: "postgres.checkpointer", Kind: metrics.TelemetryCounter,
			Query: `
				SELECT series, value, unit, '{}'::jsonb
				FROM pg_stat_checkpointer AS checkpointer
				CROSS JOIN LATERAL (VALUES
					('timed', checkpointer.num_timed::double precision, 'count'),
					('requested', checkpointer.num_requested::double precision, 'count'),
					('done', checkpointer.num_done::double precision, 'count'),
					('write_time', checkpointer.write_time::double precision, 'ms'),
					('sync_time', checkpointer.sync_time::double precision, 'ms'),
					('buffers_written', checkpointer.buffers_written::double precision, 'blocks')
				) AS metric_values(series, value, unit)`,
		},
		{
			Name: "postgres.io", Kind: metrics.TelemetryCounter,
			Query: `
				SELECT value_name, value, unit,
					jsonb_build_object(
						'backend_type', io.backend_type,
						'object', io.object,
						'context', io.context
					)
				FROM pg_stat_io AS io
				CROSS JOIN LATERAL (VALUES
					('reads', COALESCE(io.reads, 0)::double precision, 'count'),
					('read_bytes', COALESCE(io.read_bytes, 0)::double precision, 'bytes'),
					('read_time', COALESCE(io.read_time, 0)::double precision, 'ms'),
					('writes', COALESCE(io.writes, 0)::double precision, 'count'),
					('write_bytes', COALESCE(io.write_bytes, 0)::double precision, 'bytes'),
					('write_time', COALESCE(io.write_time, 0)::double precision, 'ms'),
					('writebacks', COALESCE(io.writebacks, 0)::double precision, 'count'),
					('writeback_time', COALESCE(io.writeback_time, 0)::double precision, 'ms'),
					('extends', COALESCE(io.extends, 0)::double precision, 'count'),
					('extend_bytes', COALESCE(io.extend_bytes, 0)::double precision, 'bytes'),
					('extend_time', COALESCE(io.extend_time, 0)::double precision, 'ms'),
					('hits', COALESCE(io.hits, 0)::double precision, 'count'),
					('evictions', COALESCE(io.evictions, 0)::double precision, 'count'),
					('reuses', COALESCE(io.reuses, 0)::double precision, 'count'),
					('fsyncs', COALESCE(io.fsyncs, 0)::double precision, 'count'),
					('fsync_time', COALESCE(io.fsync_time, 0)::double precision, 'ms')
				) AS metric_values(value_name, value, unit)
				WHERE value <> 0`,
		},
		{
			Name: "postgres.activity", Kind: metrics.TelemetryGauge,
			Query: `
				SELECT 'sessions', count(*)::double precision, 'sessions',
					jsonb_build_object(
						'backend_type', COALESCE(backend_type, ''),
						'state', COALESCE(state, ''),
						'wait_event_type', COALESCE(wait_event_type, ''),
						'wait_event', COALESCE(wait_event, '')
					)
				FROM pg_stat_activity
				WHERE datname = current_database() AND pid <> pg_backend_pid()
				GROUP BY backend_type, state, wait_event_type, wait_event`,
		},
	}
}

// IndexIOTelemetryQueries reports block reads and buffer hits for indexes using
// the selected PostgreSQL access methods.
func IndexIOTelemetryQueries(accessMethods ...string) []TelemetryQuery {
	if len(accessMethods) == 0 {
		return nil
	}
	return []TelemetryQuery{{
		Name: "index_io", Kind: metrics.TelemetryCounter, Args: []any{accessMethods},
		Query: `
			WITH totals AS (
				SELECT
					COALESCE(SUM(stats.idx_blks_read), 0)::bigint
						* current_setting('block_size')::bigint AS read_bytes,
					COALESCE(SUM(stats.idx_blks_hit), 0)::bigint
						* current_setting('block_size')::bigint AS hit_bytes
				FROM pg_statio_user_indexes AS stats
				JOIN pg_class AS index_relation ON index_relation.oid = stats.indexrelid
				JOIN pg_am AS access_method ON access_method.oid = index_relation.relam
				WHERE access_method.amname = ANY($1)
			)
			SELECT series, value, 'bytes', '{}'::jsonb
			FROM totals
			CROSS JOIN LATERAL (VALUES
				('read_bytes', totals.read_bytes::double precision),
				('hit_bytes', totals.hit_bytes::double precision)
			) AS metric_values(series, value)`,
	}}
}
