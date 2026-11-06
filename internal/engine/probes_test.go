package engine_test

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// idleRequests is how many probes the idle phase sends with the default config.
const idleRequests = 20

func reject(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

// rejectProbesAfter serves n probes, then rejects the rest.
func rejectProbesAfter(n int64, status int) http.HandlerFunc {
	var calls atomic.Int64
	return func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) > n {
			w.WriteHeader(status)
		}
	}
}

func TestRejectionsEndTheRun(t *testing.T) {
	const pool = 4
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		tests := map[string]func() *fakeCloudflare{
			"metadata":     func() *fakeCloudflare { return &fakeCloudflare{trace: reject(status)} },
			"idle probe":   func() *fakeCloudflare { return &fakeCloudflare{probe: rejectProbesAfter(3, status)} },
			"warm pool":    func() *fakeCloudflare { return &fakeCloudflare{probe: rejectProbesAfter(idleRequests, status)} },
			"loaded probe": func() *fakeCloudflare { return &fakeCloudflare{probe: rejectProbesAfter(idleRequests+pool, status)} },
			"download":     func() *fakeCloudflare { return &fakeCloudflare{down: reject(status)} },
			"upload":       func() *fakeCloudflare { return &fakeCloudflare{up: reject(status)} },
		}
		for name, build := range tests {
			t.Run(name+"/"+strconv.Itoa(status), func(t *testing.T) {
				cfg := fastConfig()
				cfg.Duration = 5 * time.Second
				if name == "upload" {
					cfg.Duration = 300 * time.Millisecond
				}
				begin := time.Now()
				got := run(context.Background(), cfg, build().start(t))
				wantError(t, got.Err, "Cloudflare rejected the test")
				if got.Result != nil {
					t.Error("a rejected run returned a result")
				}
				if took := time.Since(begin); took > 2*time.Second {
					t.Errorf("run ended after %v, want immediately", took)
				}
			})
		}
	}
}

// idleWithSuccesses answers the first n idle probes and fails the rest of the
// idle phase with a 500. Later probes succeed.
func idleWithSuccesses(n int64) http.HandlerFunc {
	var calls atomic.Int64
	return func(w http.ResponseWriter, _ *http.Request) {
		c := calls.Add(1)
		if c > n && c <= idleRequests {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func TestIdleSampleMinimum(t *testing.T) {
	tests := []struct {
		name      string
		successes int64
		wantErr   bool
	}{
		{"first success is discarded, leaving 4", 5, true},
		{"exactly 5 usable samples", 6, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeCloudflare{probe: idleWithSuccesses(tt.successes)}
			cfg := fastConfig()
			cfg.StallLimit = 2 * time.Second // Let every attempt happen.
			got := run(context.Background(), cfg, f.start(t))
			if tt.wantErr {
				wantError(t, got.Err, "too few latency samples: got 4")
				return
			}
			if got.Err != nil {
				t.Fatal(got.Err)
			}
		})
	}
}

// TestIdleLatencyServerTiming checks that latency comes from the probe's timing,
// less any Server-Timing durations, and survives those fields being absent.
func TestIdleLatencyServerTiming(t *testing.T) {
	const delay = 30 * time.Millisecond
	tests := []struct {
		name     string
		timing   string
		min, max float64
	}{
		{"fields missing", "", 30, 1000},
		{"fields present", "cfSpeedEdge;dur=10, cfSpeedWorker;dur=15", 5, 29},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeCloudflare{probe: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(delay)
				if tt.timing != "" {
					w.Header().Set("Server-Timing", tt.timing)
				}
			}}
			got := run(context.Background(), fastConfig(), f.start(t))
			if got.Err != nil {
				t.Fatal(got.Err)
			}
			if idle := float64(got.Result.Latency.IdleMs); idle < tt.min || idle > tt.max {
				t.Errorf("idle = %v ms, want between %v and %v", idle, tt.min, tt.max)
			}
		})
	}
}

// trackProbePeak records how many probes run at once during the download phase.
// Probes before it (idle, pool warm-up) return at once. Probes late in it aren't
// counted, so the next phase's warm-up can't overlap with the count. hold decides
// how long a counted probe lasts.
func trackProbePeak(f *fakeCloudflare, peak *atomic.Int64, hold func(*http.Request)) {
	var downStart, current atomic.Int64
	f.down = func(w http.ResponseWriter, r *http.Request) {
		downStart.CompareAndSwap(0, time.Now().UnixNano())
		f.stream(w, r, 0)
	}
	f.probe = func(_ http.ResponseWriter, r *http.Request) {
		started := downStart.Load()
		if started == 0 {
			return
		}
		if time.Since(time.Unix(0, started)) < 150*time.Millisecond {
			c := current.Add(1)
			defer current.Add(-1)
			for {
				p := peak.Load()
				if c <= p || peak.CompareAndSwap(p, c) {
					break
				}
			}
		}
		hold(r)
	}
}

func TestProbesOverlap(t *testing.T) {
	var peak atomic.Int64
	f := &fakeCloudflare{}
	trackProbePeak(f, &peak, func(*http.Request) { time.Sleep(35 * time.Millisecond) })
	got := run(context.Background(), fastConfig(), f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if p := peak.Load(); p < 2 || p > 4 {
		t.Errorf("peak concurrent probes = %d, want 2 to 4", p)
	}
}

func TestExhaustedProbePool(t *testing.T) {
	var peak atomic.Int64
	f := &fakeCloudflare{}
	trackProbePeak(f, &peak, func(r *http.Request) { f.block(r) })
	cfg := fastConfig()
	cfg.PoolWarmTimeout = 50 * time.Millisecond
	got := run(context.Background(), cfg, f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if p := peak.Load(); p != 4 {
		t.Errorf("peak concurrent probes = %d, want the pool size of 4", p)
	}
	lat := got.Result.Latency
	if lat.LoadedDownloadMs == nil {
		t.Fatal("loaded latency is nil, want the elapsed time of the cut-off probes")
	}
	if !lat.LoadedDownloadLowerBound {
		t.Error("loaded latency isn't marked as a lower bound")
	}
}

func TestNullLoadedLatency(t *testing.T) {
	var calls atomic.Int64
	f := &fakeCloudflare{probe: func(http.ResponseWriter, *http.Request) {
		if calls.Add(1) > idleRequests {
			panic(http.ErrAbortHandler) // A transport error for every later probe.
		}
	}}
	cfg := fastConfig()
	// Probes at 100 and 200 ms have failed long before the 250 ms deadline, and the
	// next would be after it, so none can be in flight when the phase ends.
	cfg.Duration = 250 * time.Millisecond
	cfg.ProbeInterval = 100 * time.Millisecond
	got := run(context.Background(), cfg, f.start(t))
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if d, u := got.Result.Latency.LoadedDownloadMs, got.Result.Latency.LoadedUploadMs; d != nil || u != nil {
		t.Errorf("loaded = %v, %v, want nil", d, u)
	}
}
