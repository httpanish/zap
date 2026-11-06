package output

import "math"

var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// Resample reduces values to at most width points, averaging each bucket. Values
// that already fit are returned unchanged, since stretching them would invent
// data.
func Resample(values []float64, width int) []float64 {
	if width <= 0 {
		return nil
	}
	if len(values) <= width {
		return values
	}
	out := make([]float64, width)
	for i := range out {
		lo := i * len(values) / width
		hi := (i + 1) * len(values) / width
		var sum float64
		for _, v := range values[lo:hi] {
			sum += v
		}
		out[i] = sum / float64(hi-lo)
	}
	return out
}

// Sparkline renders values in at most width columns, scaled from zero to the
// highest value.
func Sparkline(values []float64, width int) string {
	values = Resample(values, width)
	var highest float64
	for _, v := range values {
		highest = max(highest, v)
	}
	out := make([]rune, len(values))
	for i, v := range values {
		level := 0
		if highest > 0 {
			level = int(math.Round(v / highest * float64(len(sparkLevels)-1)))
		}
		out[i] = sparkLevels[level]
	}
	return string(out)
}
