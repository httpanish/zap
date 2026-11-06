package engine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// idle measures latency over one warm connection. A failed request is dropped,
// and jitter uses consecutive surviving samples.
func (r *runner) idle(ctx context.Context) (idleMs, jitterMs float64, err error) {
	r.emit(PhaseStarted{PhaseIdle})
	conn := r.backend.NewConn()
	defer conn.Close()

	start := r.cfg.Now()
	var (
		samples     []float64
		discarded   bool
		lastSuccess time.Time
		lastErr     error
	)
	// remaining is how long the next attempt may run: the first-byte allowance
	// until something has arrived, then the stall limit.
	remaining := func() time.Duration {
		if lastSuccess.IsZero() {
			return r.cfg.FirstByteAllowance - r.cfg.Now().Sub(start)
		}
		return r.cfg.StallLimit - r.cfg.Now().Sub(lastSuccess)
	}
	stalled := func() error {
		if lastSuccess.IsZero() {
			return withLastErr(fmt.Errorf("no response for %s during %s", r.cfg.FirstByteAllowance, PhaseIdle), lastErr)
		}
		return withLastErr(fmt.Errorf("connection dropped for %s during %s", r.cfg.StallLimit, PhaseIdle), lastErr)
	}

	for i := range r.cfg.IdleAttempts {
		left := remaining()
		if left <= 0 {
			return 0, 0, stalled()
		}
		actx, cancel := context.WithTimeout(ctx, left)
		d, err := conn.Probe(actx)
		cancel()
		r.emitProgress(Progress{Phase: PhaseIdle, Fraction: float64(i+1) / float64(r.cfg.IdleAttempts)})

		switch {
		case ctx.Err() != nil:
			return 0, 0, ctx.Err()
		case err == nil:
			lastSuccess = r.cfg.Now()
			if !discarded {
				discarded = true // The first success includes connection setup.
				continue
			}
			samples = append(samples, toMs(d))
		case errors.Is(err, ErrRejected):
			return 0, 0, err
		default:
			lastErr = err
			if i < r.cfg.IdleAttempts-1 && !sleep(ctx, r.cfg.RetryDelay) {
				return 0, 0, ctx.Err()
			}
		}
	}

	if len(samples) < r.cfg.MinIdleSamples {
		return 0, 0, withLastErr(fmt.Errorf("too few latency samples: got %d, need at least %d", len(samples), r.cfg.MinIdleSamples), lastErr)
	}
	idleMs, jitterMs = Median(samples), Jitter(samples)
	r.emit(IdleDone{IdleMs: idleMs, JitterMs: jitterMs})
	return idleMs, jitterMs, nil
}
