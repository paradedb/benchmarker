package dashboard

import (
	"math"
	"testing"
	"time"
)

func TestLatencyHistogramUsesNearestRankAtMicrosecondResolution(t *testing.T) {
	var histogram latencyHistogram
	for microseconds := 1; microseconds <= 100; microseconds++ {
		histogram.record(float64(microseconds) * latencyResolutionMs)
	}

	got := histogram.percentiles()
	for name, values := range map[string][2]float64{
		"mean": {got.mean, 0.0505},
		"p50":  {got.p50, 0.050},
		"p90":  {got.p90, 0.090},
		"p95":  {got.p95, 0.095},
		"p99":  {got.p99, 0.099},
	} {
		if math.Abs(values[0]-values[1]) > 1e-12 {
			t.Fatalf("%s = %.12fms, want %.12fms", name, values[0], values[1])
		}
	}
}

func TestLatencyHistogramUpperBoundIsConservativeAndTight(t *testing.T) {
	for _, value := range []float64{0.0001, 0.2551, 1, 10, 100, 1_000, 60_000} {
		upper := latencyHistogramUpperBound(latencyHistogramIndex(value))
		if upper < value {
			t.Fatalf("bucket upper bound %.9fms understates %.9fms", upper, value)
		}
		maxError := latencyResolutionMs
		if value > float64(latencyLinearBucketCount-1)*latencyResolutionMs {
			maxError = value/128 + latencyResolutionMs
		}
		if upper-value > maxError {
			t.Fatalf("bucket error %.9fms for %.9fms exceeds %.9fms", upper-value, value, maxError)
		}
	}
}

func TestLatencyHistogramRecordRemoveAndPercentilesDoNotAllocate(t *testing.T) {
	var histogram latencyHistogram
	for i := 1; i <= 10_000; i++ {
		histogram.record(float64(i) / 10)
	}

	allocations := testing.AllocsPerRun(100, func() {
		histogram.record(42)
		histogram.remove(42)
		_ = histogram.percentiles()
	})
	if allocations != 0 {
		t.Fatalf("bounded histogram operations allocated %.2f objects per refresh", allocations)
	}
}

func TestQueryTimelineExpiresSamplesAtWindowCutoff(t *testing.T) {
	query := newQueryMetrics("query")
	query.recordLatency(10, 1_000)
	query.recordLatency(20, 1_500)
	query.recordLatency(30, 2_000)

	output := &Output{timelineWindow: time.Second}
	output.updateQueryTimeline(query)

	point := query.Timeline[0]
	if point.Count != 2 {
		t.Fatalf("timeline count = %d, want 2", point.Count)
	}
	if point.Mean != 25 {
		t.Fatalf("timeline mean = %.3fms, want 25ms", point.Mean)
	}
	if query.timelineWindowStart != 1 {
		t.Fatalf("window start index = %d, want 1", query.timelineWindowStart)
	}
	if point.P50 < 20 || point.P50 > 20.2 {
		t.Fatalf("timeline p50 = %.3fms, want the bucket containing 20ms", point.P50)
	}
	if point.P99 < 30 || point.P99 > 30.3 {
		t.Fatalf("timeline p99 = %.3fms, want the bucket containing 30ms", point.P99)
	}
}

func TestRecordLatencyKeepsRawAndLiveStatsInSync(t *testing.T) {
	query := newQueryMetrics("query")
	query.recordLatency(10.125, 2_000)
	query.recordLatency(5.25, 1_000)

	if got := len(query.Latencies); got != 2 {
		t.Fatalf("raw latency count = %d, want 2", got)
	}
	if got := query.liveStats.histogram.count; got != 2 {
		t.Fatalf("live histogram count = %d, want 2", got)
	}
	if query.Latencies[0] != 10.125 || query.Latencies[1] != 5.25 {
		t.Fatalf("raw latencies changed: %v", query.Latencies)
	}
	if query.Timestamps[0] != 2_000 || query.Timestamps[1] != 2_000 {
		t.Fatalf("timestamps are not monotonic: %v", query.Timestamps)
	}
	if query.liveStats.min != 5.25 || query.liveStats.max != 10.125 {
		t.Fatalf("live min/max = %.3f/%.3f, want 5.25/10.125", query.liveStats.min, query.liveStats.max)
	}
	if got := query.liveStats.histogram.percentiles().mean; got != 7.6875 {
		t.Fatalf("live mean = %.4f, want 7.6875", got)
	}
}

func TestBroadcastWithoutClientsDoesNoSummaryWork(t *testing.T) {
	output := &Output{
		liveEnabled: true,
		clients:     make(map[chan []byte]struct{}),
	}

	// A summary attempt would panic because data is intentionally nil.
	output.broadcast()
}
