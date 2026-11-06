package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/cloudflare"
	"github.com/httpanish/zap/internal/engine"
)

// harness runs commands against a local server, with history in a temp dir.
type harness struct {
	t        *testing.T
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	history  string
	requests atomic.Int64
	status   atomic.Int64 // If set, every request gets this status.
	app      *app
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, history: filepath.Join(t.TempDir(), "data", "history.jsonl")}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)
	h.app = &app{
		stdout:        &h.stdout,
		stderr:        &h.stderr,
		terminalWidth: func() (int, bool) { return 0, false },
		location:      time.UTC,
		historyPath:   func() (string, error) { return h.history, nil },
		newClient: func() *cloudflare.Client {
			return cloudflare.New(cloudflare.Config{Version: "test", BaseURL: srv.URL, TransferBytes: 100_000,
				Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }})
		},
		tuneEngine: func(c *engine.Config) {
			c.Duration = 100 * time.Millisecond
			c.SampleInterval = 10 * time.Millisecond
			c.ProbeInterval = 10 * time.Millisecond
		},
	}
	return h
}

func (h *harness) serve(w http.ResponseWriter, r *http.Request) {
	h.requests.Add(1)
	if s := h.status.Load(); s != 0 {
		w.WriteHeader(int(s))
		return
	}
	switch r.URL.Path {
	case "/cdn-cgi/trace":
		io.WriteString(w, "colo=TST\n")
	case "/__up":
		io.Copy(io.Discard, r.Body)
	default:
		if r.URL.Query().Get("bytes") != "0" {
			w.Write(make([]byte, 100_000))
		}
	}
}

func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.stdout.Reset()
	h.stderr.Reset()
	return h.app.run(args)
}

func (h *harness) savedRuns() int {
	h.t.Helper()
	data, err := os.ReadFile(h.history)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func TestUsageErrorsExitBeforeAnyTraffic(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string][]string{
		"duration too short": {"--duration", "2s"},
		"duration too long":  {"--duration", "61s"},
		"duration not valid": {"--duration", "soon"},
		"no streams":         {"--streams", "0"},
		"too many streams":   {"--streams", "33"},
		"unknown flag":       {"--nope"},
		"unknown argument":   {"nope"},
		"card dir missing":   {"--card-dir", filepath.Join(t.TempDir(), "missing")},
		"card dir not a dir": {"--card", "--card-dir", notADir},
		"history limit zero": {"history", "--limit", "0"},
		"history bad flag":   {"history", "--nope"},
		"history extra arg":  {"history", "extra"},
		"version extra arg":  {"version", "extra"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if code := h.run(args...); code != exitUsage {
				t.Errorf("exit = %d, want %d; stderr: %s", code, exitUsage, h.stderr.String())
			}
			if n := h.requests.Load(); n != 0 {
				t.Errorf("sent %d requests before failing", n)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", h.stdout.String())
			}
		})
	}
}

func TestDurationAndStreamLimitsAreInclusive(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"--duration", "3s"}, {"--duration", "60s"}, {"--streams", "1"}, {"--streams", "32"}} {
		if code := h.run(append(args, "--no-save")...); code != exitOK {
			t.Errorf("%v: exit = %d; stderr: %s", args, code, h.stderr.String())
		}
	}
}

func TestPlainMode(t *testing.T) {
	h := newHarness(t)
	if code := h.run(); code != exitOK {
		t.Fatalf("exit = %d; stderr: %s", code, h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"Download", "Upload", "Latency", "Loaded", "Server      Cloudflare TST", "Data used"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("stdout has escape sequences: %q", out)
	}
	if h.stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing when it's redirected", h.stderr.String())
	}
	if h.savedRuns() != 1 {
		t.Errorf("saved %d runs, want 1", h.savedRuns())
	}
}

func TestProgressLinesOnlyWhenStderrIsATerminal(t *testing.T) {
	h := newHarness(t)
	h.app.stderrIsTTY = true
	if code := h.run("--no-save"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	want := "Connecting...\nMeasuring latency...\nMeasuring download...\nMeasuring upload...\n"
	if h.stderr.String() != want {
		t.Errorf("stderr = %q, want %q", h.stderr.String(), want)
	}
	if strings.Contains(h.stdout.String(), "Measuring") {
		t.Error("progress lines reached stdout")
	}
}

func TestJSONMode(t *testing.T) {
	h := newHarness(t)
	if code := h.run("--json"); code != exitOK {
		t.Fatalf("exit = %d; stderr: %s", code, h.stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout isn't JSON: %v\n%s", err, h.stdout.String())
	}
	for _, key := range []string{"schema_version", "timestamp", "server", "config", "download", "upload", "latency"} {
		if _, ok := got[key]; !ok {
			t.Errorf("JSON is missing %q", key)
		}
	}
	if h.savedRuns() != 1 {
		t.Errorf("saved %d runs, want 1", h.savedRuns())
	}
}

func TestNoSave(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"--no-save"}, {"--no-save", "--json"}} {
		if code := h.run(args...); code != exitOK {
			t.Fatalf("exit = %d", code)
		}
	}
	if h.savedRuns() != 0 {
		t.Errorf("saved %d runs, want 0", h.savedRuns())
	}
}

func TestRejectionExitsWithClearError(t *testing.T) {
	for _, status := range []int64{403, 429} {
		h := newHarness(t)
		h.status.Store(status)
		if code := h.run("--json"); code != exitFailure {
			t.Errorf("exit = %d, want 1", code)
		}
		if h.stdout.Len() != 0 {
			t.Errorf("stdout = %q, want empty on failure", h.stdout.String())
		}
		if !strings.Contains(h.stderr.String(), "Cloudflare rejected the test") {
			t.Errorf("stderr = %q", h.stderr.String())
		}
		if h.savedRuns() != 0 {
			t.Error("a failed run was saved")
		}
	}
}

func TestSaveFailureWarnsButDoesNotFail(t *testing.T) {
	h := newHarness(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.history = filepath.Join(file, "history.jsonl") // Can't be created under a file.
	if code := h.run(); code != exitOK {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(h.stderr.String(), "could not save") {
		t.Errorf("stderr = %q, want a warning", h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Download") {
		t.Error("the result wasn't printed")
	}
}

var cardPath = regexp.MustCompile(`zap-\d{4}-\d{2}-\d{2}-\d{6}\.png`)

func cardsIn(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "zap-*.png"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestCardDirTurnsOnTheCard(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if code := h.run("--json", "--no-save", "--card-dir", dir); code != exitOK {
		t.Fatalf("exit = %d; stderr: %s", code, h.stderr.String())
	}
	if cards := cardsIn(t, dir); len(cards) != 1 {
		t.Fatalf("cards = %v, want 1", cards)
	}
	if !cardPath.MatchString(h.stderr.String()) {
		t.Errorf("stderr = %q, want the card's path", h.stderr.String())
	}
	if cardPath.MatchString(h.stdout.String()) {
		t.Error("the card's path reached stdout")
	}
}

func TestCardDefaultsToTheCurrentDirectory(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if code := h.run("--card", "--no-save"); code != exitOK {
		t.Fatalf("exit = %d; stderr: %s", code, h.stderr.String())
	}
	if cards := cardsIn(t, dir); len(cards) != 1 {
		t.Errorf("cards = %v, want 1", cards)
	}
}

func TestCardFailureAfterTheTestLosesNothing(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "cards")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The directory passes the check before the test, then disappears.
	tune := h.app.tuneEngine
	h.app.tuneEngine = func(c *engine.Config) {
		tune(c)
		os.Remove(dir)
	}
	if code := h.run("--json", "--card-dir", dir); code != exitFailure {
		t.Errorf("exit = %d, want 1", code)
	}
	if !json.Valid(h.stdout.Bytes()) || h.stdout.Len() == 0 {
		t.Errorf("stdout = %q, want valid JSON", h.stdout.String())
	}
	if h.savedRuns() != 1 {
		t.Errorf("saved %d runs, want 1", h.savedRuns())
	}
	if !strings.Contains(h.stderr.String(), "could not write the card") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("zap created the card directory")
	}
}

func TestProxyWarningOmitsCredentials(t *testing.T) {
	h := newHarness(t)
	proxy, _ := url.Parse("http://bob:s3cret@127.0.0.1:1")
	h.app.newClient = func() *cloudflare.Client {
		return cloudflare.New(cloudflare.Config{Version: "test", BaseURL: "http://192.0.2.1",
			Proxy: func(*http.Request) (*url.URL, error) { return proxy, nil }})
	}
	h.app.tuneEngine = func(c *engine.Config) { c.ConnectTimeout = 200 * time.Millisecond }
	if code := h.run("--no-save"); code != exitFailure {
		t.Errorf("exit = %d, want 1", code)
	}
	got := h.stderr.String()
	if !strings.Contains(got, "Using proxy 127.0.0.1:1: results measure the proxy path") {
		t.Errorf("stderr = %q", got)
	}
	if strings.Contains(got, "s3cret") || strings.Contains(got, "bob") {
		t.Errorf("stderr leaks credentials: %q", got)
	}
}

func TestHistoryCommand(t *testing.T) {
	h := newHarness(t)
	if code := h.run(); code != exitOK {
		t.Fatal(code)
	}
	if code := h.run("history"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if out := h.stdout.String(); !strings.Contains(out, "DATE") || !strings.Contains(out, "TST") || !strings.Contains(out, "(oldest → newest)") {
		t.Errorf("table:\n%s", out)
	}
	if code := h.run("history", "--json", "--limit", "1"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var runs []map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &runs); err != nil || len(runs) != 1 {
		t.Errorf("runs = %v, err %v, want 1 run", runs, err)
	}
}

func TestHistoryMissingFile(t *testing.T) {
	h := newHarness(t)
	if code := h.run("history"); code != exitOK {
		t.Errorf("exit = %d, want 0", code)
	}
	if h.stdout.Len() != 0 || h.stderr.String() != "No history yet\n" {
		t.Errorf("stdout %q, stderr %q", h.stdout.String(), h.stderr.String())
	}
	if code := h.run("history", "--json"); code != exitOK {
		t.Errorf("exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(h.stdout.String()); got != "[]" {
		t.Errorf("stdout = %q, want []", got)
	}
}

func TestHistoryUnreadableFile(t *testing.T) {
	h := newHarness(t)
	h.history = t.TempDir() // A directory opens, but can't be read.
	if code := h.run("history"); code != exitFailure {
		t.Errorf("exit = %d, want 1", code)
	}
}

func TestHistorySkippedLinesWarnOnce(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Dir(h.history), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.history, []byte("junk\nmore junk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := h.run("history", "--json"); code != exitOK {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(h.stderr.String(), "skipped 2 unreadable history lines") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
	if got := strings.TrimSpace(h.stdout.String()); got != "[]" {
		t.Errorf("stdout = %q, want []", got)
	}
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	if code := h.run("version"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.HasPrefix(h.stdout.String(), "zap ") {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}
