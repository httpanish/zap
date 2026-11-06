package output

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/httpanish/zap/internal/engine"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func dec(v engine.Dec1) *engine.Dec1 { return &v }

// specResult is the example result from the specification.
func specResult() *engine.Result {
	return &engine.Result{
		SchemaVersion: 1,
		Timestamp:     time.Date(2026, 11, 6, 23, 50, 0, 0, time.UTC),
		Server:        engine.Server{Provider: "cloudflare", Colo: "LHR"},
		Config:        engine.RunConfig{DurationSeconds: 10, Streams: 4},
		Download: engine.PhaseResult{Mbps: 412.3, Bytes: 449800000,
			SamplesMbps: []engine.Dec1{110.0, 190.0, 398.2, 409.6, 415.3, 418.1, 412.7, 416.4, 411.5, 416.6}},
		Upload: engine.PhaseResult{Mbps: 48.1, Bytes: 55600000,
			SamplesMbps: []engine.Dec1{22.4, 37.6, 46.2, 47.8, 48.5, 48.9, 48.3, 48.6, 47.9, 48.6}},
		Latency: engine.Latency{IdleMs: 12.0, JitterMs: 2.1, LoadedDownloadMs: dec(50.2), LoadedUploadMs: dec(91.4)},
	}
}

func TestSummaryGolden(t *testing.T) {
	tests := []struct {
		file   string
		modify func(*engine.Result)
	}{
		{"summary.golden", func(*engine.Result) {}},
		{"summary_null_loaded.golden", func(r *engine.Result) { r.Latency.LoadedDownloadMs, r.Latency.LoadedUploadMs = nil, nil }},
		{"summary_lower_bound.golden", func(r *engine.Result) { r.Latency.LoadedUploadLowerBound = true }},
		{"summary_gigabit.golden", func(r *engine.Result) { r.Download.Mbps = 1412.3 }},
		{"summary_unknown_colo.golden", func(r *engine.Result) { r.Server.Colo = "unknown" }},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			r := specResult()
			tt.modify(r)
			var b bytes.Buffer
			if err := Summary(&b, r); err != nil {
				t.Fatal(err)
			}
			golden(t, tt.file, b.Bytes())
		})
	}
}

func TestJSONGolden(t *testing.T) {
	var b bytes.Buffer
	if err := JSON(&b, specResult()); err != nil {
		t.Fatal(err)
	}
	golden(t, "result.json.golden", b.Bytes())
}

func TestJSONNullLoadedAndNoExtraFields(t *testing.T) {
	r := specResult()
	r.Latency.LoadedDownloadMs = nil
	r.Latency.LoadedDownloadLowerBound = true
	var b bytes.Buffer
	if err := JSON(&b, r); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Latency map[string]any `json:"latency"`
	}
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got.Latency["loaded_download_ms"]; !ok || v != nil {
		t.Errorf("loaded_download_ms = %v, present %v, want an explicit null", v, ok)
	}
	if len(got.Latency) != 4 {
		t.Errorf("latency has fields %v, want the 4 in the schema", got.Latency)
	}
}

func historyRuns() []engine.Result {
	newer := *specResult()
	older := *specResult()
	older.Timestamp = time.Date(2026, 11, 5, 9, 12, 0, 0, time.UTC)
	older.Download.Mbps, older.Upload.Mbps, older.Latency.IdleMs = 398.0, 47.2, 14
	return []engine.Result{newer, older}
}

func TestHistoryTableGolden(t *testing.T) {
	var b bytes.Buffer
	if err := HistoryTable(&b, historyRuns(), 80, time.UTC); err != nil {
		t.Fatal(err)
	}
	golden(t, "history.golden", b.Bytes())
}

func TestHistoryTableUsesLocalTime(t *testing.T) {
	var b bytes.Buffer
	if err := HistoryTable(&b, historyRuns(), 80, time.FixedZone("X", 3600)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "2026-11-07 00:50") {
		t.Errorf("table isn't in the given zone:\n%s", b.String())
	}
}

func TestHistoryTableWideValuesKeepColumnsApart(t *testing.T) {
	runs := historyRuns()
	runs[0].Download.Mbps = 12345.6
	var b bytes.Buffer
	if err := HistoryTable(&b, runs, 80, time.UTC); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "12345.6 Mbps  48.1 Mbps") {
		t.Errorf("columns ran together:\n%s", b.String())
	}
}

func TestHistoryTrendRunsOldestToNewest(t *testing.T) {
	runs := historyRuns()
	runs[0].Download.Mbps = 100 // Newest.
	runs[1].Download.Mbps = 0   // Oldest.
	var b bytes.Buffer
	if err := HistoryTable(&b, runs, 80, time.UTC); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "Download ▁█") {
		t.Errorf("trend = %q, want low then high", strings.SplitN(b.String(), "\n", 2)[0])
	}
}

func TestHistoryTrendFitsTheWidth(t *testing.T) {
	var runs []engine.Result
	for range 200 {
		runs = append(runs, *specResult())
	}
	for _, width := range []int{80, 120, 40} {
		var b bytes.Buffer
		if err := HistoryTable(&b, runs, width, time.UTC); err != nil {
			t.Fatal(err)
		}
		line, _, _ := strings.Cut(b.String(), "\n")
		if n := utf8.RuneCountInString(line); n > width {
			t.Errorf("width %d: trend line is %d columns", width, n)
		}
	}
}

func TestResample(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		width  int
		want   []float64
	}{
		{"empty", nil, 5, nil},
		{"fits, so unchanged", []float64{1, 2}, 5, []float64{1, 2}},
		{"exact fit", []float64{1, 2}, 2, []float64{1, 2}},
		{"halves by averaging", []float64{1, 3, 5, 7}, 2, []float64{2, 6}},
		{"uneven buckets", []float64{2, 4, 6}, 2, []float64{2, 5}},
		{"down to one", []float64{1, 2, 3}, 1, []float64{2}},
		{"no width", []float64{1, 2}, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resample(tt.values, tt.width); !slices.Equal(got, tt.want) {
				t.Errorf("Resample() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSparkline(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		width  int
		want   string
	}{
		{"empty", nil, 10, ""},
		{"scaled from zero to the highest", []float64{0, 50, 100}, 10, "▁▅█"},
		{"not from the lowest", []float64{90, 100}, 10, "▇█"},
		{"all zero stays on the floor", []float64{0, 0}, 10, "▁▁"},
		{"resampled to the width", []float64{0, 0, 100, 100}, 2, "▁█"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sparkline(tt.values, tt.width); got != tt.want {
				t.Errorf("Sparkline() = %q, want %q", got, tt.want)
			}
		})
	}
}
