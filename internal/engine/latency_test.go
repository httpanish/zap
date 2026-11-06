package engine

import "testing"

func TestMedian(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"empty", nil, 0},
		{"one", []float64{7}, 7},
		{"odd count, unsorted", []float64{9, 1, 5}, 5},
		{"even count averages the middle two", []float64{4, 1, 3, 2}, 2.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Median(tt.values); got != tt.want {
				t.Errorf("Median() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMedianLeavesInputUnsorted(t *testing.T) {
	values := []float64{3, 1, 2}
	Median(values)
	if values[0] != 3 {
		t.Errorf("Median reordered its input: %v", values)
	}
}

func TestJitter(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"empty", nil, 0},
		{"one", []float64{5}, 0},
		{"steady", []float64{5, 5, 5}, 0},
		{"order matters: consecutive differences", []float64{10, 14, 12}, 3},
		{"sorted values give a different answer", []float64{10, 12, 14}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Jitter(tt.values); got != tt.want {
				t.Errorf("Jitter() = %v, want %v", got, tt.want)
			}
		})
	}
}
