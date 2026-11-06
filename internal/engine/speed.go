package engine

import "time"

// Sample is the cumulative byte count at a moment in a phase.
type Sample struct {
	Elapsed time.Duration
	Bytes   int64
}

func mbps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / d.Seconds() / 1e6
}

// Headline returns the headline speed: bytes transferred after the ramp-up
// window × 8, divided by the time actually elapsed in that window. Samples must
// be in time order.
func Headline(samples []Sample, ramp time.Duration) float64 {
	if len(samples) < 2 {
		return 0
	}
	last := samples[len(samples)-1]
	for _, s := range samples {
		if s.Elapsed >= ramp {
			return mbps(last.Bytes-s.Bytes, last.Elapsed-s.Elapsed)
		}
	}
	return 0
}

// Current returns the throughput over the trailing window, measured from the
// newest sample at least that old, or from the oldest sample if none is.
func Current(samples []Sample, window time.Duration) float64 {
	if len(samples) < 2 {
		return 0
	}
	last := samples[len(samples)-1]
	base := samples[0]
	for i := len(samples) - 2; i >= 0; i-- {
		base = samples[i]
		if last.Elapsed-base.Elapsed >= window {
			break
		}
	}
	return mbps(last.Bytes-base.Bytes, last.Elapsed-base.Elapsed)
}

// bytesAt linearly interpolates the cumulative byte count at time t.
func bytesAt(samples []Sample, t time.Duration) float64 {
	for i, s := range samples {
		if s.Elapsed < t {
			continue
		}
		if i == 0 || s.Elapsed == t {
			return float64(s.Bytes)
		}
		prev := samples[i-1]
		frac := float64(t-prev.Elapsed) / float64(s.Elapsed-prev.Elapsed)
		return float64(prev.Bytes) + frac*float64(s.Bytes-prev.Bytes)
	}
	return float64(samples[len(samples)-1].Bytes)
}

// PerSecond returns one rounded throughput value per whole second of the phase,
// including ramp-up. A final partial second is dropped. The result is never nil.
func PerSecond(samples []Sample) []Dec1 {
	out := []Dec1{}
	if len(samples) < 2 {
		return out
	}
	whole := int(samples[len(samples)-1].Elapsed / time.Second)
	for s := 1; s <= whole; s++ {
		delta := bytesAt(samples, time.Duration(s)*time.Second) - bytesAt(samples, time.Duration(s-1)*time.Second)
		out = append(out, Round1(delta*8/1e6))
	}
	return out
}
