package engine_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func wantError(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Errorf("err = %v, want it to contain %q", err, substr)
	}
}

func TestMidPhaseFailure(t *testing.T) {
	f := &fakeCloudflare{}
	var first atomic.Bool
	f.down = func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			w.Write(make([]byte, 16<<10))
			w.(http.Flusher).Flush()
		}
		panic(http.ErrAbortHandler) // Drops the connection.
	}
	got := run(context.Background(), fastConfig(), f.start(t))
	wantError(t, got.Err, "during download")
}

func TestStalledDownload(t *testing.T) {
	f := &fakeCloudflare{}
	f.down = func(w http.ResponseWriter, r *http.Request) { f.stream(w, r, 20*time.Millisecond) }
	cfg := fastConfig()
	cfg.Duration = 2 * time.Second

	begin := time.Now()
	got := run(context.Background(), cfg, f.start(t))
	wantError(t, got.Err, "connection dropped for 150ms during download")
	if took := time.Since(begin); took > time.Second {
		t.Errorf("failing took %v, want it well before the 2s deadline", took)
	}
}

func TestStalledUpload(t *testing.T) {
	f := &fakeCloudflare{}
	f.up = func(w http.ResponseWriter, r *http.Request) { f.block(r) } // Never reads the body.
	cfg := fastConfig()
	cfg.Duration = 2 * time.Second
	got := run(context.Background(), cfg, f.start(t))
	wantError(t, got.Err, "connection dropped for 150ms during upload")
}

func TestStallAtDeadlineFails(t *testing.T) {
	f := &fakeCloudflare{}
	// Quiet for the last 80 ms of the phase, past the 50 ms threshold, and never resumes.
	f.down = func(w http.ResponseWriter, r *http.Request) { f.stream(w, r, 120*time.Millisecond) }
	got := run(context.Background(), fastConfig(), f.start(t))
	wantError(t, got.Err, "connection dropped for 150ms during download")
}

func TestStallAtDeadlineThatResumesCompletes(t *testing.T) {
	var before atomic.Int64
	f := &fakeCloudflare{}
	f.down = func(w http.ResponseWriter, r *http.Request) {
		// Send for 120 ms, go quiet across the deadline, then send a big burst
		// that must not count.
		flusher := w.(http.Flusher)
		chunk := make([]byte, 16<<10)
		for begin := time.Now(); time.Since(begin) < 120*time.Millisecond; time.Sleep(2 * time.Millisecond) {
			n, err := w.Write(chunk)
			if err != nil {
				return
			}
			flusher.Flush()
			before.Add(int64(n))
		}
		time.Sleep(130 * time.Millisecond)
		for range 500 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		flusher.Flush()
		f.block(r)
	}
	got := run(context.Background(), fastConfig(), f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if b := got.Result.Download.Bytes; b <= 0 || b > before.Load() {
		t.Errorf("counted %d bytes, want at most the %d sent before the deadline", b, before.Load())
	}
}

func TestShortGapIsANormalResult(t *testing.T) {
	f := &fakeCloudflare{}
	f.down = func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		for range 3 {
			if _, err := w.Write(make([]byte, 16<<10)); err != nil {
				return
			}
			flusher.Flush()
			time.Sleep(100 * time.Millisecond) // Under the 150 ms stall limit.
		}
		f.stream(w, r, 0)
	}
	cfg := fastConfig()
	cfg.Duration = 400 * time.Millisecond
	got := run(context.Background(), cfg, f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
}

func TestNoFirstByte(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		maxTime  time.Duration
	}{
		{"allowance ends first", 5 * time.Second, 2 * time.Second},
		{"deadline ends first, and nothing could count", 200 * time.Millisecond, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeCloudflare{}
			f.down = func(w http.ResponseWriter, r *http.Request) { f.block(r) }
			cfg := fastConfig()
			cfg.Duration = tt.duration
			begin := time.Now()
			got := run(context.Background(), cfg, f.start(t))
			wantError(t, got.Err, "no data received")
			wantError(t, got.Err, "during download")
			if took := time.Since(begin); took > tt.maxTime {
				t.Errorf("failing took %v", took)
			}
		})
	}
}

func TestServerErrorsAreRetried(t *testing.T) {
	var calls atomic.Int64
	f := &fakeCloudflare{}
	f.down = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.stream(w, r, 0)
	}
	got := run(context.Background(), fastConfig(), f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
}

func TestPersistentServerErrorsNameTheCause(t *testing.T) {
	f := &fakeCloudflare{down: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}}
	got := run(context.Background(), fastConfig(), f.start(t))
	wantError(t, got.Err, "no data received")
	wantError(t, got.Err, "HTTP 502")
}
