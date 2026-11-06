package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	rampFraction  = 0.2
	currentWindow = time.Second
)

type transferOut struct {
	result     PhaseResult
	loaded     *float64
	lowerBound bool
}

// transferRun is the state of one transfer phase.
type transferRun struct {
	r       *runner
	phase   Phase
	parent  context.Context
	ctx     context.Context
	start   time.Time
	meter   *meter
	probes  *prober
	lastErr atomic.Pointer[error]
	samples []Sample
	series  []float64
}

func (r *runner) transfer(parent context.Context, phase Phase, op transferOp) (*transferOut, error) {
	cfg := r.cfg
	r.emit(PhaseStarted{phase})

	warmStart := cfg.Now()
	pool, err := r.warmPool(parent)
	defer closeAll(pool)
	if err != nil {
		return nil, err
	}
	// The allowance covers all of the phase's connection setup.
	allowance := cfg.FirstByteAllowance - cfg.Now().Sub(warmStart)

	ctx, cancel := context.WithCancelCause(parent)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel(nil) // Deferred calls run in reverse, so this precedes the Wait.

	start := cfg.Now()
	ramp := time.Duration(float64(cfg.Duration) * rampFraction)
	t := &transferRun{
		r: r, phase: phase, parent: parent, ctx: ctx, start: start,
		meter:   &meter{now: cfg.Now, start: start, duration: cfg.Duration},
		samples: []Sample{{}},
		series:  []float64{},
	}
	for range cfg.Streams {
		conn := r.backend.NewConn()
		workers.Go(func() { r.work(ctx, cancel, conn, op, t.meter, &t.lastErr) })
	}
	t.probes = newProber(ctx, cfg, start, ramp, pool, func(err error) { cancel(err) })
	defer t.probes.stop(cfg.Duration)

	ticker := time.NewTicker(cfg.SampleInterval)
	defer ticker.Stop()

	if err := t.sample(ticker, allowance); err != nil {
		return nil, err
	}

	// The deadline has passed, and the meter excludes any later bytes.
	t.record(cfg.Duration)
	total := t.samples[len(t.samples)-1].Bytes
	loaded, lowerBound := t.probes.stop(cfg.Duration)
	if total == 0 {
		return nil, t.noData(cfg.Duration) // Nothing could count, however long we wait.
	}
	if err := t.grace(ticker); err != nil {
		return nil, err
	}

	r.baseUsed += total
	headline := Headline(t.samples, ramp)
	r.emit(TransferDone{
		Phase:            phase,
		Mbps:             headline,
		Series:           t.series,
		LoadedMs:         loaded,
		LoadedLowerBound: lowerBound,
		TotalBytes:       r.baseUsed,
	})
	return &transferOut{
		result:     PhaseResult{Mbps: Round1(headline), Bytes: total, SamplesMbps: PerSecond(t.samples)},
		loaded:     loaded,
		lowerBound: lowerBound,
	}, nil
}

// sample records throughput and watches for stalls until the phase's deadline.
func (t *transferRun) sample(ticker *time.Ticker, allowance time.Duration) error {
	cfg := t.r.cfg
	deadline := time.NewTimer(cfg.Duration)
	defer deadline.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return t.failure()
		case <-deadline.C:
			return nil
		case <-ticker.C:
			el := cfg.Now().Sub(t.start)
			if el >= cfg.Duration {
				return nil
			}
			if at, ok := t.meter.last(); !ok {
				if el >= allowance {
					return t.noData(el)
				}
			} else if el-at >= cfg.StallLimit {
				return t.dropped()
			}
			t.record(el)
			t.r.emitProgress(Progress{
				Phase:      t.phase,
				Fraction:   float64(el) / float64(cfg.Duration),
				Mbps:       Current(t.samples, currentWindow),
				Series:     t.series[:len(t.series):len(t.series)], // Append-only, so safe to share.
				LoadedMs:   t.probes.current(),
				TotalBytes: t.r.baseUsed + t.samples[len(t.samples)-1].Bytes,
			})
		}
	}
}

// grace runs after the deadline. If the link was quiet when the deadline hit, the
// stall clock keeps running: bytes resuming completes the phase, and reaching the
// limit fails the run.
func (t *transferRun) grace(ticker *time.Ticker) error {
	cfg := t.r.cfg
	if at, _ := t.meter.last(); cfg.Duration-at < cfg.StallLimit/3 {
		if t.ctx.Err() != nil {
			return t.failure()
		}
		return nil
	}
	for {
		select {
		case <-t.ctx.Done():
			return t.failure()
		case <-ticker.C:
			at, _ := t.meter.last()
			if at >= cfg.Duration {
				return nil
			}
			if cfg.Now().Sub(t.start)-at >= cfg.StallLimit {
				return t.dropped()
			}
		}
	}
}

func (t *transferRun) record(el time.Duration) {
	total := t.meter.total.Load()
	prev := t.samples[len(t.samples)-1]
	t.samples = append(t.samples, Sample{Elapsed: el, Bytes: total})
	t.series = append(t.series, mbps(total-prev.Bytes, el-prev.Elapsed))
}

func (t *transferRun) failure() error {
	if err := t.parent.Err(); err != nil {
		return err
	}
	return context.Cause(t.ctx)
}

func (t *transferRun) last() error {
	if p := t.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

func (t *transferRun) dropped() error {
	return withLastErr(fmt.Errorf("connection dropped for %s during %s", t.r.cfg.StallLimit, t.phase), t.last())
}

func (t *transferRun) noData(after time.Duration) error {
	return withLastErr(fmt.Errorf("no data received for %s during %s", after.Round(100*time.Millisecond), t.phase), t.last())
}
