package engine_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

func TestNormalRun(t *testing.T) {
	f := &fakeCloudflare{}
	got := run(context.Background(), fastConfig(), f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	r := got.Result
	if r.SchemaVersion != 1 || r.Server.Provider != "cloudflare" || r.Server.Colo != "TST" {
		t.Errorf("header fields = %+v", r)
	}
	if r.Config.Streams != 2 || r.Config.DurationSeconds != 0.2 {
		t.Errorf("config = %+v", r.Config)
	}
	if r.Timestamp.IsZero() || r.Timestamp.Location() != time.UTC {
		t.Errorf("timestamp = %v, want UTC", r.Timestamp)
	}
	for name, p := range map[string]engine.PhaseResult{"download": r.Download, "upload": r.Upload} {
		if p.Mbps <= 0 || p.Bytes <= 0 {
			t.Errorf("%s = %+v, want positive figures", name, p)
		}
		if p.SamplesMbps == nil {
			t.Errorf("%s samples are nil, want empty", name)
		}
	}
	if r.Latency.LoadedDownloadMs == nil || r.Latency.LoadedUploadMs == nil {
		t.Errorf("loaded latency = %+v, want values", r.Latency)
	}
}

func TestEventOrder(t *testing.T) {
	f := &fakeCloudflare{}
	var phases []engine.Phase
	var done []engine.Phase
	var last engine.Event
	for ev := range engine.Start(context.Background(), fastConfig(), f.start(t)) {
		last = ev
		switch e := ev.(type) {
		case engine.PhaseStarted:
			phases = append(phases, e.Phase)
		case engine.TransferDone:
			done = append(done, e.Phase)
		}
	}
	want := []engine.Phase{engine.PhaseConnect, engine.PhaseIdle, engine.PhaseDownload, engine.PhaseUpload}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
	if len(done) != 2 || done[0] != engine.PhaseDownload || done[1] != engine.PhaseUpload {
		t.Errorf("transfers done = %v", done)
	}
	if _, ok := last.(engine.Finished); !ok {
		t.Errorf("last event = %T, want Finished", last)
	}
}

func TestMissingColoContinues(t *testing.T) {
	f := &fakeCloudflare{trace: func(http.ResponseWriter, *http.Request) {}}
	got := run(context.Background(), fastConfig(), f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if got.Result.Server.Colo != "unknown" {
		t.Errorf("colo = %q, want unknown", got.Result.Server.Colo)
	}
}

func TestCancellation(t *testing.T) {
	f := &fakeCloudflare{}
	cfg := fastConfig()
	cfg.Duration = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	begin := time.Now()
	events := engine.Start(ctx, cfg, f.start(t))
	var finished engine.Finished
	for ev := range events {
		switch e := ev.(type) {
		case engine.PhaseStarted:
			if e.Phase == engine.PhaseDownload {
				time.AfterFunc(50*time.Millisecond, cancel)
			}
		case engine.Finished:
			finished = e
		}
	}
	if !errors.Is(finished.Err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", finished.Err)
	}
	if finished.Result != nil {
		t.Error("a cancelled run returned a result")
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Errorf("cancelling took %v", took)
	}
}
