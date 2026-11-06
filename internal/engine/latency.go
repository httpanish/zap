package engine

import (
	"math"
	"slices"
	"time"
)

// Median returns the median of values, or 0 if there are none.
func Median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Jitter returns the mean absolute difference between consecutive values.
func Jitter(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(values); i++ {
		sum += math.Abs(values[i] - values[i-1])
	}
	return sum / float64(len(values)-1)
}

func toMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
