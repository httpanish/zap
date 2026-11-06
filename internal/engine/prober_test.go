package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeConn answers probes with a fixed result. If block is set, Probe waits for
// its context to end.
type fakeConn struct {
	sample time.Duration
	err    error
	block  bool
	calls  *atomic.Int64
}

func (f *fakeConn) Probe(ctx context.Context) (time.Duration, error) {
	if f.calls != nil {
		f.calls.Add(1)
	}
	if f.block {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	return f.sample, f.err
}
func (f *fakeConn) Download(context.Context, func(int)) error { return nil }
func (f *fakeConn) Upload(context.Context, func(int)) error   { return nil }
func (f *fakeConn) Close()                                    {}

func poolOf(n int, c Conn) []Conn {
	pool := make([]Conn, n)
	for i := range pool {
		pool[i] = c
	}
	return pool
}

func runProber(t *testing.T, ramp, length time.Duration, pool []Conn, fatal func(error)) (*float64, bool) {
	t.Helper()
	cfg := Config{ProbeInterval: 10 * time.Millisecond}.withDefaults()
	p := newProber(context.Background(), cfg, cfg.Now(), ramp, pool, fatal)
	time.Sleep(length)
	return p.stop(length)
}

func noFatal(t *testing.T) func(error) {
	return func(err error) { t.Errorf("unexpected fatal error: %v", err) }
}

func TestProberMedianOfCompletedProbes(t *testing.T) {
	loaded, lower := runProber(t, 0, 100*time.Millisecond, poolOf(2, &fakeConn{sample: 40 * time.Millisecond}), noFatal(t))
	if loaded == nil || *loaded != 40 {
		t.Fatalf("loaded = %v, want 40", loaded)
	}
	if lower {
		t.Error("lowerBound = true with every probe completed")
	}
}

// currentAfter reads the running median, which only completed probes affect, so
// a probe that happens to be in flight when the test ends can't make it flaky.
func currentAfter(t *testing.T, ramp, length time.Duration, pool []Conn) *float64 {
	t.Helper()
	cfg := Config{ProbeInterval: 10 * time.Millisecond}.withDefaults()
	p := newProber(context.Background(), cfg, cfg.Now(), ramp, pool, noFatal(t))
	defer p.stop(length)
	time.Sleep(length)
	return p.current()
}

func TestProberExcludesProbesSentDuringRampUp(t *testing.T) {
	if got := currentAfter(t, time.Hour, 60*time.Millisecond, poolOf(2, &fakeConn{sample: time.Millisecond})); got != nil {
		t.Errorf("current = %v, want nil", *got)
	}
}

func TestProberIgnoresProbesSentAfterTheDeadline(t *testing.T) {
	cfg := Config{ProbeInterval: time.Millisecond}.withDefaults()
	p := newProber(context.Background(), cfg, cfg.Now(), 0, poolOf(1, &fakeConn{block: true}), noFatal(t))
	time.Sleep(20 * time.Millisecond)
	if loaded, _ := p.stop(0); loaded != nil {
		t.Errorf("loaded = %v, want nil", *loaded)
	}
}

func TestProberCountsInFlightProbesAsLowerBound(t *testing.T) {
	loaded, lower := runProber(t, 0, 100*time.Millisecond, poolOf(3, &fakeConn{block: true}), noFatal(t))
	if loaded == nil || *loaded <= 0 {
		t.Fatalf("loaded = %v, want the elapsed time of the cut-off probes", loaded)
	}
	if !lower {
		t.Error("lowerBound = false with every sample cut off")
	}
}

func TestProberSkipsWhenThePoolIsExhausted(t *testing.T) {
	var calls atomic.Int64
	runProber(t, 0, 100*time.Millisecond, poolOf(2, &fakeConn{block: true, calls: &calls}), noFatal(t))
	if got := calls.Load(); got != 2 {
		t.Errorf("probes sent = %d, want 2 (one per pooled connection)", got)
	}
}

func TestProberFallsBackToRampUpProbesWhenThePoolIsHeld(t *testing.T) {
	loaded, lower := runProber(t, 50*time.Millisecond, 100*time.Millisecond, poolOf(2, &fakeConn{block: true}), noFatal(t))
	if loaded == nil {
		t.Fatal("loaded = nil, want the elapsed time of the probes holding the pool")
	}
	if !lower {
		t.Error("lowerBound = false, want true")
	}
}

func TestProberDropsTransportErrors(t *testing.T) {
	if got := currentAfter(t, 0, 60*time.Millisecond, poolOf(2, &fakeConn{err: errors.New("reset")})); got != nil {
		t.Errorf("current = %v, want nil", *got)
	}
}

func TestProberReportsRejectionAsFatal(t *testing.T) {
	var fatal atomic.Bool
	runProber(t, 0, 60*time.Millisecond, poolOf(1, &fakeConn{err: ErrRejected}), func(err error) {
		if errors.Is(err, ErrRejected) {
			fatal.Store(true)
		}
	})
	if !fatal.Load() {
		t.Error("rejection was not reported as fatal")
	}
}
