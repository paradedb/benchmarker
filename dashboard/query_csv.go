package dashboard

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
)

const (
	defaultQueryCSVMaxSeries = 100_000
	maxQueryCSVQueryIDBytes  = 1_024
)

// streamingQuantile is a constant-space P² quantile estimator. The first five
// observations use exact nearest-rank values; later observations update five
// marker heights without retaining samples.
type streamingQuantile struct {
	quantile  float64
	count     uint64
	initial   [5]float64
	heights   [5]float64
	positions [5]int64
}

func newStreamingQuantile(quantile float64) streamingQuantile {
	return streamingQuantile{quantile: quantile}
}

func (q *streamingQuantile) record(value float64) {
	if q.count < uint64(len(q.initial)) {
		q.initial[q.count] = value
		q.count++
		if q.count == uint64(len(q.initial)) {
			copy(q.heights[:], q.initial[:])
			sort.Float64s(q.heights[:])
			for i := range q.positions {
				q.positions[i] = int64(i + 1)
			}
		}
		return
	}

	q.count++
	cell := 0
	switch {
	case value < q.heights[0]:
		q.heights[0] = value
	case value >= q.heights[4]:
		q.heights[4] = value
		cell = 3
	default:
		for cell = 0; cell < 4; cell++ {
			if value < q.heights[cell+1] {
				break
			}
		}
	}
	for i := cell + 1; i < len(q.positions); i++ {
		q.positions[i]++
	}

	desired := q.desiredPositions()
	for i := 1; i <= 3; i++ {
		delta := desired[i] - float64(q.positions[i])
		step := int64(0)
		if delta >= 1 && q.positions[i+1]-q.positions[i] > 1 {
			step = 1
		} else if delta <= -1 && q.positions[i-1]-q.positions[i] < -1 {
			step = -1
		}
		if step == 0 {
			continue
		}

		candidate := q.parabolic(i, step)
		if candidate <= q.heights[i-1] || candidate >= q.heights[i+1] {
			candidate = q.linear(i, step)
		}
		q.heights[i] = candidate
		q.positions[i] += step
	}
}

func (q *streamingQuantile) desiredPositions() [5]float64 {
	n := float64(q.count - 1)
	return [5]float64{
		1,
		1 + n*q.quantile/2,
		1 + n*q.quantile,
		1 + n*(1+q.quantile)/2,
		float64(q.count),
	}
}

func (q *streamingQuantile) parabolic(index int, step int64) float64 {
	current := q.positions[index]
	previous := q.positions[index-1]
	next := q.positions[index+1]
	stepFloat := float64(step)

	return q.heights[index] + stepFloat/float64(next-previous)*(float64(current-previous+step)/float64(next-current)*(q.heights[index+1]-q.heights[index])+
		float64(next-current-step)/float64(current-previous)*(q.heights[index]-q.heights[index-1]))
}

func (q *streamingQuantile) linear(index int, step int64) float64 {
	neighbor := index + int(step)
	return q.heights[index] + float64(step)*(q.heights[neighbor]-q.heights[index])/
		float64(q.positions[neighbor]-q.positions[index])
}

func (q *streamingQuantile) value() float64 {
	if q.count == 0 {
		return 0
	}
	if q.count > uint64(len(q.initial)) {
		return q.heights[2]
	}

	values := q.initial
	observed := values[:q.count]
	sort.Float64s(observed)
	index := int(math.Ceil(float64(len(observed))*q.quantile)) - 1
	if index < 0 {
		index = 0
	}
	return observed[index]
}

type queryCSVStats struct {
	count uint64
	sum   float64
	min   float64
	max   float64
	p50   streamingQuantile
	p90   streamingQuantile
	p95   streamingQuantile
	p99   streamingQuantile
}

func newQueryCSVStats() *queryCSVStats {
	return &queryCSVStats{
		p50: newStreamingQuantile(0.50),
		p90: newStreamingQuantile(0.90),
		p95: newStreamingQuantile(0.95),
		p99: newStreamingQuantile(0.99),
	}
}

func (s *queryCSVStats) record(value float64) {
	if s.count == 0 {
		s.min = value
		s.max = value
	} else {
		if value < s.min {
			s.min = value
		}
		if value > s.max {
			s.max = value
		}
	}
	s.count++
	s.sum += value
	s.p50.record(value)
	s.p90.record(value)
	s.p95.record(value)
	s.p99.record(value)
}

func dashboardQueryCSVMaxSeries(enabled bool) (int, error) {
	if !enabled {
		return 0, nil
	}
	raw := os.Getenv("DASHBOARD_QUERY_CSV_MAX_SERIES")
	if raw == "" {
		return defaultQueryCSVMaxSeries, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("DASHBOARD_QUERY_CSV_MAX_SERIES must be a positive integer")
	}
	return value, nil
}

// recordQueryCSV records a query latency without retaining the raw sample. The
// caller holds Output.mu. The global cap bounds cardinality across all runs.
func (o *Output) recordQueryCSV(run *RunMetrics, queryID string, latency float64) {
	if queryID == "" {
		return
	}
	if len(queryID) > maxQueryCSVQueryIDBytes {
		o.queryCSVDropped++
		if o.queryCSVError == "" {
			o.queryCSVError = fmt.Sprintf("query_id exceeds the %d-byte query_csv limit", maxQueryCSVQueryIDBytes)
		}
		return
	}
	if run.QueryCSV == nil {
		run.QueryCSV = make(map[string]*queryCSVStats)
	}
	stats := run.QueryCSV[queryID]
	if stats == nil {
		if o.queryCSVSeries >= o.queryCSVMaxSeries {
			o.queryCSVDropped++
			if o.queryCSVError == "" {
				o.queryCSVError = fmt.Sprintf("query_csv exceeded DASHBOARD_QUERY_CSV_MAX_SERIES=%d", o.queryCSVMaxSeries)
			}
			return
		}
		stats = newQueryCSVStats()
		run.QueryCSV[queryID] = stats
		o.queryCSVSeries++
	}
	stats.record(latency)
}

type queryCSVRow struct {
	run     string
	backend string
	queryID string
	count   uint64
	min     float64
	mean    float64
	p50     float64
	p90     float64
	p95     float64
	p99     float64
	max     float64
}

func marshalQueryCSV(data *DashboardData) ([]byte, error) {
	rows := make([]queryCSVRow, 0)
	for runName, run := range data.Runs {
		for queryID, stats := range run.QueryCSV {
			if stats.count == 0 {
				continue
			}
			rows = append(rows, queryCSVRow{
				run:     runName,
				backend: run.Backend,
				queryID: queryID,
				count:   stats.count,
				min:     stats.min,
				mean:    stats.sum / float64(stats.count),
				p50:     stats.p50.value(),
				p90:     stats.p90.value(),
				p95:     stats.p95.value(),
				p99:     stats.p99.value(),
				max:     stats.max,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].p50 != rows[j].p50 {
			return rows[i].p50 > rows[j].p50
		}
		if rows[i].p95 != rows[j].p95 {
			return rows[i].p95 > rows[j].p95
		}
		if rows[i].max != rows[j].max {
			return rows[i].max > rows[j].max
		}
		if rows[i].run != rows[j].run {
			return rows[i].run < rows[j].run
		}
		return rows[i].queryID < rows[j].queryID
	})

	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	if err := writer.Write([]string{
		"rank", "run", "backend", "query_id", "count", "min_ms", "mean_ms",
		"p50_ms", "p90_ms", "p95_ms", "p99_ms", "max_ms",
	}); err != nil {
		return nil, err
	}
	format := func(value float64) string {
		return strconv.FormatFloat(value, 'f', 3, 64)
	}
	for index, row := range rows {
		if err := writer.Write([]string{
			strconv.Itoa(index + 1), row.run, row.backend, row.queryID,
			strconv.FormatUint(row.count, 10), format(row.min), format(row.mean),
			format(row.p50), format(row.p90), format(row.p95), format(row.p99),
			format(row.max),
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
