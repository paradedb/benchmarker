package dashboard

import (
	"math"
	"math/bits"
)

const (
	latencyResolutionMs      = 0.001 // one microsecond
	latencySubBucketBits     = 7
	latencyLinearBucketCount = 1 << (latencySubBucketBits + 1)
	latencyHistogramBinCount = 4096
)

// latencyHistogram is a fixed-size, base-2 histogram with 128 subdivisions
// per power of two. Recording and removal are O(1), and reading all live
// percentiles is O(latencyHistogramBinCount), independent of run duration.
// The running sum retains an exact mean for the active samples.
//
// A percentile is reported as its bucket's upper bound. It therefore never
// understates the selected sample; bucket width is one microsecond through 255
// microseconds and less than 1/128 of the value above that point.
type latencyHistogram struct {
	bins  [latencyHistogramBinCount]uint64
	count uint64
	sum   float64
}

type latencyPercentiles struct {
	mean float64
	p50  float64
	p90  float64
	p95  float64
	p99  float64
}

func (h *latencyHistogram) record(value float64) {
	h.bins[latencyHistogramIndex(value)]++
	h.count++
	h.sum += value
}

func (h *latencyHistogram) remove(value float64) {
	index := latencyHistogramIndex(value)
	if h.bins[index] == 0 {
		panic("dashboard: removing absent latency histogram sample")
	}
	h.bins[index]--
	h.count--
	h.sum -= value
	if h.count == 0 {
		h.sum = 0
	}
}

func (h *latencyHistogram) reset() {
	clear(h.bins[:])
	h.count = 0
	h.sum = 0
}

func (h *latencyHistogram) percentiles() latencyPercentiles {
	if h.count == 0 {
		return latencyPercentiles{}
	}

	ranks := [...]uint64{
		(h.count*50 + 99) / 100,
		(h.count*90 + 99) / 100,
		(h.count*95 + 99) / 100,
		(h.count*99 + 99) / 100,
	}
	values := [len(ranks)]float64{}
	next := 0
	var seen uint64
	for index, count := range h.bins {
		seen += count
		for next < len(ranks) && seen >= ranks[next] {
			values[next] = latencyHistogramUpperBound(index)
			next++
		}
		if next == len(ranks) {
			break
		}
	}

	return latencyPercentiles{
		mean: h.sum / float64(h.count),
		p50:  values[0],
		p90:  values[1],
		p95:  values[2],
		p99:  values[3],
	}
}

func latencyHistogramIndex(value float64) int {
	if value <= 0 || math.IsNaN(value) {
		return 0
	}
	if value >= latencyHistogramUpperBound(latencyHistogramBinCount-1) {
		return latencyHistogramBinCount - 1
	}

	scaled := uint64(math.Ceil(value / latencyResolutionMs))
	if scaled < latencyLinearBucketCount {
		return int(scaled)
	}

	shift := bits.Len64(scaled>>latencySubBucketBits) - 1
	index := (shift << latencySubBucketBits) + int(scaled>>shift)
	if index >= latencyHistogramBinCount {
		return latencyHistogramBinCount - 1
	}
	return index
}

func latencyHistogramUpperBound(index int) float64 {
	if index < latencyLinearBucketCount {
		return float64(index) * latencyResolutionMs
	}

	shift := (index >> latencySubBucketBits) - 1
	subBucket := uint64((1 << latencySubBucketBits) + (index & ((1 << latencySubBucketBits) - 1)))
	maxScaled := ((subBucket + 1) << shift) - 1
	return float64(maxScaled) * latencyResolutionMs
}

// liveLatencyStats keeps exact mean/min/max values alongside approximate live
// percentiles. Raw samples remain the source of truth for exact JSON exports.
type liveLatencyStats struct {
	histogram latencyHistogram
	min       float64
	max       float64
}

func (s *liveLatencyStats) record(value float64) {
	if s.histogram.count == 0 {
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
	s.histogram.record(value)
}
