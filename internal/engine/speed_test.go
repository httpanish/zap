package engine

import (
	"math"
	"slices"
	"testing"
	"time"
)

const sec = time.Second

func TestHeadline(t *testing.T) {
	tests := []struct {
		name    string
		samples []Sample
		ramp    time.Duration
		want    float64
	}{
		{"no samples", nil, 2 * sec, 0},
		{"one sample", []Sample{{0, 0}}, 0, 0},
		{
			"excludes ramp-up bytes",
			[]Sample{{0, 0}, {2 * sec, 999_000_000}, {4 * sec, 999_000_000 + 2_500_000}},
			2 * sec,
			10,
		},
		{
			"uses actual elapsed time, not nominal ticks",
			[]Sample{{0, 0}, {1 * sec, 0}, {3500 * time.Millisecond, 2_500_000}},
			1 * sec,
			8,
		},
		{
			"window starts at the first sample past ramp-up",
			[]Sample{{0, 0}, {900 * time.Millisecond, 100}, {1100 * time.Millisecond, 1_000_000}, {2100 * time.Millisecond, 2_000_000}},
			1 * sec,
			8,
		},
		{"no sample past ramp-up", []Sample{{0, 0}, {1 * sec, 100}}, 2 * sec, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Headline(tt.samples, tt.ramp); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("Headline() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCurrent(t *testing.T) {
	tests := []struct {
		name    string
		samples []Sample
		want    float64
	}{
		{"one sample", []Sample{{0, 0}}, 0},
		{"shorter than the window", []Sample{{0, 0}, {500 * time.Millisecond, 1_000_000}}, 16},
		{"only the trailing window counts", []Sample{{0, 0}, {1 * sec, 50_000_000}, {2 * sec, 51_000_000}}, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Current(tt.samples, sec); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("Current() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPerSecond(t *testing.T) {
	tests := []struct {
		name    string
		samples []Sample
		want    []Dec1
	}{
		{"no samples is empty, not nil", nil, []Dec1{}},
		{"under a second", []Sample{{0, 0}, {900 * time.Millisecond, 100}}, []Dec1{}},
		{"exact seconds", []Sample{{0, 0}, {1 * sec, 1_000_000}, {2 * sec, 3_000_000}}, []Dec1{8, 16}},
		{"interpolates between ticks", []Sample{{0, 0}, {2 * sec, 2_000_000}}, []Dec1{8, 8}},
		{"drops a final partial second", []Sample{{0, 0}, {1 * sec, 1_000_000}, {1500 * time.Millisecond, 9_000_000}}, []Dec1{8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PerSecond(tt.samples)
			if got == nil || !slices.Equal(got, tt.want) {
				t.Errorf("PerSecond() = %v, want %v", got, tt.want)
			}
		})
	}
}
