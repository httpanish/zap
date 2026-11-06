package card

import (
	"bytes"
	"flag"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

var update = flag.Bool("update", false, "rewrite golden files")

func dec(v engine.Dec1) *engine.Dec1 { return &v }

func sample() *engine.Result {
	return &engine.Result{
		SchemaVersion: 1,
		Timestamp:     time.Date(2026, 11, 6, 23, 50, 7, 0, time.UTC),
		Server:        engine.Server{Provider: "cloudflare", Colo: "LHR"},
		Download: engine.PhaseResult{Mbps: 412.3, Bytes: 449800000,
			SamplesMbps: []engine.Dec1{110.0, 190.0, 398.2, 409.6, 415.3, 418.1, 412.7, 416.4, 411.5, 416.6}},
		Upload: engine.PhaseResult{Mbps: 48.1, Bytes: 55600000,
			SamplesMbps: []engine.Dec1{22.4, 37.6, 46.2, 47.8, 48.5, 48.9, 48.3, 48.6, 47.9, 48.6}},
		Latency: engine.Latency{IdleMs: 12, JitterMs: 2.1, LoadedDownloadMs: dec(50.2), LoadedUploadMs: dec(91.4)},
	}
}

func TestNewLayout(t *testing.T) {
	l := NewLayout(sample(), time.FixedZone("X", 3600))
	checks := map[string][2]string{
		"Download":       {l.Download, "412.3"},
		"Upload":         {l.Upload, "48.1"},
		"Idle":           {l.Idle, "12.0 ms"},
		"Jitter":         {l.Jitter, "2.1 ms"},
		"LoadedDownload": {l.LoadedDownload, "50.2 ms"},
		"LoadedUpload":   {l.LoadedUpload, "91.4 ms"},
		"Date":           {l.Date, "7 Nov 2026, 00:50"},
		"Server":         {l.Server, "Cloudflare · LHR"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}
	if len(l.DownloadSeries) != 10 || l.DownloadSeries[0] != 110 || len(l.UploadSeries) != 10 {
		t.Errorf("series aren't from samples_mbps: %v, %v", l.DownloadSeries, l.UploadSeries)
	}
}

func TestNewLayoutLoadedEdgeCases(t *testing.T) {
	r := sample()
	r.Latency.LoadedDownloadMs = nil
	r.Latency.LoadedUploadLowerBound = true
	l := NewLayout(r, time.UTC)
	if l.LoadedDownload != "n/a" {
		t.Errorf("LoadedDownload = %q, want n/a", l.LoadedDownload)
	}
	if l.LoadedUpload != ">=91.4 ms" {
		t.Errorf("LoadedUpload = %q, want >=91.4 ms", l.LoadedUpload)
	}
}

// TestRenderGolden compares every pixel, since the embedded fonts should make the
// card identical on every machine.
func TestRenderGolden(t *testing.T) {
	var got bytes.Buffer
	if err := png.Encode(&got, Render(NewLayout(sample(), time.UTC))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "card.golden.png")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("rendered card differs from %s; if intended, rerun with -update", path)
	}
}

func TestRenderHandlesEmptyAndZeroSeries(t *testing.T) {
	l := NewLayout(sample(), time.UTC)
	l.DownloadSeries, l.UploadSeries = nil, []float64{0, 0}
	if b := Render(l).Bounds(); b.Dx() != 1200 || b.Dy() != 630 {
		t.Errorf("size = %v", b)
	}
}

func TestRenderManyBarsStayInsideThePanel(t *testing.T) {
	l := NewLayout(sample(), time.UTC)
	l.DownloadSeries = make([]float64, 60)
	for i := range l.DownloadSeries {
		l.DownloadSeries[i] = 100
	}
	img := Render(l)
	// The gap between the panels must stay background-coloured.
	x := margin + panelWidth + panelGap/2
	if got := img.RGBAAt(x, 300); got != surface {
		t.Errorf("pixel between panels = %v, want the surface colour", got)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path, err := Write(dir, sample(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "zap-2026-11-06-235007.png"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 630 {
		t.Errorf("size = %v, want 1200×630", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir holds %d files, want only the card", len(entries))
	}
}

func TestWriteFailureLeavesNothingBehind(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Write(missing, sample(), time.UTC); err == nil {
		t.Fatal("want an error for a missing directory")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("Write created the directory")
	}
}

func TestFilenameUsesTheGivenZone(t *testing.T) {
	got := Filename(sample().Timestamp, time.FixedZone("X", 3600))
	if got != "zap-2026-11-07-005007.png" {
		t.Errorf("Filename = %q", got)
	}
	if !regexp.MustCompile(`^zap-\d{4}-\d{2}-\d{2}-\d{6}\.png$`).MatchString(got) {
		t.Errorf("Filename = %q has the wrong shape", got)
	}
}

func TestCheckDir(t *testing.T) {
	dir := t.TempDir()
	if err := CheckDir(dir); err != nil {
		t.Errorf("writable dir: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("CheckDir left %d files behind", len(entries))
	}

	missing := filepath.Join(dir, "missing")
	if err := CheckDir(missing); err == nil {
		t.Error("missing dir: want an error")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("CheckDir created the directory")
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(file); err == nil {
		t.Error("regular file: want an error")
	}

	readOnly := filepath.Join(dir, "ro")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() > 0 { // Root and Windows ignore these permissions.
		if err := CheckDir(readOnly); err == nil {
			t.Error("read-only dir: want an error")
		}
	}
}
