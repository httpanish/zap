package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

func run(at string, colo string) *engine.Result {
	ts, err := time.Parse(time.RFC3339, at)
	if err != nil {
		panic(err)
	}
	return &engine.Result{
		SchemaVersion: engine.SchemaVersion,
		Timestamp:     ts,
		Server:        engine.Server{Provider: "cloudflare", Colo: colo},
		Download:      engine.PhaseResult{Mbps: 412.3, SamplesMbps: []engine.Dec1{110, 190.5}},
		Upload:        engine.PhaseResult{Mbps: 48.1, SamplesMbps: []engine.Dec1{}},
	}
}

func colos(runs []engine.Result) string {
	var out []string
	for _, r := range runs {
		out = append(out, r.Server.Colo)
	}
	return strings.Join(out, ",")
}

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(t *testing.T, r *engine.Result) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestAppendThenRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "dir", "history.jsonl")
	if err := Append(path, run("2026-11-06T10:00:00Z", "AAA")); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, run("2026-11-07T10:00:00Z", "BBB")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 2 {
		t.Errorf("file has %d lines, want 2", lines)
	}
	runs, skipped, err := Read(path, 20)
	if err != nil || skipped != 0 {
		t.Fatalf("Read: skipped %d, err %v", skipped, err)
	}
	if got := colos(runs); got != "BBB,AAA" {
		t.Errorf("runs = %s, want BBB,AAA", got)
	}
	if runs[1].Download.Mbps != 412.3 || len(runs[1].Download.SamplesMbps) != 2 {
		t.Errorf("run didn't round-trip: %+v", runs[1].Download)
	}
}

func TestConcurrentAppendsKeepWholeLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if err := Append(path, run("2026-11-06T10:00:00Z", "AAA")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	runs, skipped, err := Read(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 50 || skipped != 0 {
		t.Errorf("got %d runs and %d skipped, want 50 and 0", len(runs), skipped)
	}
}

func TestSortKeepsFileOrderForTies(t *testing.T) {
	path := write(t, line(t, run("2026-11-06T10:00:00Z", "OLD"))+
		line(t, run("2026-11-07T10:00:00Z", "TIE1"))+
		line(t, run("2026-11-07T10:00:00Z", "TIE2"))+
		line(t, run("2026-11-08T10:00:00Z", "NEW")))
	runs, _, err := Read(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := colos(runs); got != "NEW,TIE1,TIE2,OLD" {
		t.Errorf("order = %s", got)
	}
}

func TestLimitIgnoresSkippedLines(t *testing.T) {
	path := write(t, "garbage\n"+
		line(t, run("2026-11-06T10:00:00Z", "AAA"))+
		"garbage\n"+
		line(t, run("2026-11-07T10:00:00Z", "BBB"))+
		line(t, run("2026-11-08T10:00:00Z", "CCC")))
	runs, skipped, err := Read(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := colos(runs); got != "CCC,BBB" {
		t.Errorf("runs = %s, want the two newest valid runs", got)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
}

func TestMissingFile(t *testing.T) {
	runs, skipped, err := Read(filepath.Join(t.TempDir(), "nope.jsonl"), 20)
	if err != nil || runs != nil || skipped != 0 {
		t.Errorf("Read = %v, %d, %v, want nothing and no error", runs, skipped, err)
	}
}

func TestUnreadableFile(t *testing.T) {
	// A directory opens but can't be read, on every platform and for every user.
	if _, _, err := Read(t.TempDir(), 20); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want a read error", err)
	}
}

func TestSkippedLines(t *testing.T) {
	future := run("2026-11-06T10:00:00Z", "FUT")
	future.SchemaVersion = 2
	tests := []struct {
		name string
		text string
	}{
		{"not JSON", "not json\n"},
		{"truncated JSON", `{"schema_version":1,"timestamp":"2026-11` + "\n"},
		{"wrong field type", `{"schema_version":1,"timestamp":"2026-11-06T10:00:00Z","download":{"mbps":"fast"}}` + "\n"},
		{"unknown schema version", line(t, future)},
		{"no schema version", `{"timestamp":"2026-11-06T10:00:00Z"}` + "\n"},
		{"no timestamp", `{"schema_version":1}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs, skipped, err := Read(write(t, tt.text), 20)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 0 || skipped != 1 {
				t.Errorf("got %d runs and %d skipped, want 0 and 1", len(runs), skipped)
			}
		})
	}
}

func TestBlankLinesAndMissingFinalNewline(t *testing.T) {
	path := write(t, "\n\n"+strings.TrimSuffix(line(t, run("2026-11-06T10:00:00Z", "AAA")), "\n"))
	runs, skipped, err := Read(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || skipped != 0 {
		t.Errorf("got %d runs and %d skipped, want 1 and 0", len(runs), skipped)
	}
}

func TestNullSamplesBecomeEmpty(t *testing.T) {
	path := write(t, `{"schema_version":1,"timestamp":"2026-11-06T10:00:00Z"}`+"\n")
	runs, _, err := Read(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Download.SamplesMbps == nil || runs[0].Upload.SamplesMbps == nil {
		t.Error("samples are nil, want empty")
	}
}

func TestLoadedLatencyNullRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := Append(path, run("2026-11-06T10:00:00Z", "AAA")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"loaded_download_ms":null`) {
		t.Errorf("line = %s, want an explicit null", data)
	}
	runs, _, err := Read(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].Latency.LoadedDownloadMs != nil {
		t.Error("null didn't round-trip")
	}
}

func TestResolvePath(t *testing.T) {
	home := func() (string, error) { return "/home/u", nil }
	noHome := func() (string, error) { return "", errors.New("no home") }
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	tests := []struct {
		name    string
		goos    string
		vars    map[string]string
		home    func() (string, error)
		want    string
		wantErr bool
	}{
		{"linux with XDG", "linux", map[string]string{"XDG_DATA_HOME": "/data"}, home, filepath.Join("/data", "zap", "history.jsonl"), false},
		{"linux fallback", "linux", nil, home, filepath.Join("/home/u", ".local", "share", "zap", "history.jsonl"), false},
		{"linux ignores relative XDG", "linux", map[string]string{"XDG_DATA_HOME": "rel"}, home, filepath.Join("/home/u", ".local", "share", "zap", "history.jsonl"), false},
		{"linux without a home", "linux", nil, noHome, "", true},
		{"macOS", "darwin", nil, home, filepath.Join("/home/u", "Library", "Application Support", "zap", "history.jsonl"), false},
		{"Windows", "windows", map[string]string{"LocalAppData": "C:/AppData"}, home, filepath.Join("C:/AppData", "zap", "history.jsonl"), false},
		{"Windows without the variable", "windows", nil, home, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePath(tt.goos, env(tt.vars), tt.home)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Errorf("path = %q, want %q", got, tt.want)
			}
		})
	}
}
