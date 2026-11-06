// Package engine runs the test sequence, takes the measurements, and reports
// them as a stream of events. It knows nothing about how they are displayed, or
// about the endpoints behind its Backend.
package engine

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Start runs a test in the background and returns its events. The last event is
// always Finished, and the channel closes after it. Cancelling ctx ends the run
// with ctx's error.
func Start(ctx context.Context, cfg Config, backend Backend) <-chan Event {
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		r := &runner{cfg: cfg.withDefaults(), backend: backend, events: ch}
		res, err := r.run(ctx)
		if err != nil {
			ch <- Finished{Err: err}
			return
		}
		ch <- Finished{Result: res}
	}()
	return ch
}

type runner struct {
	cfg      Config
	backend  Backend
	events   chan Event
	baseUsed int64 // Payload bytes from completed phases.
}

func (r *runner) emit(e Event) { r.events <- e }

// emitProgress never blocks: if the buffer is full, this update is dropped and
// the next one replaces it.
func (r *runner) emitProgress(p Progress) {
	select {
	case r.events <- p:
	default:
	}
}

func (r *runner) run(ctx context.Context) (*Result, error) {
	meta, err := r.connect(ctx)
	if err != nil {
		return nil, err
	}
	idleMs, jitterMs, err := r.idle(ctx)
	if err != nil {
		return nil, err
	}
	down, err := r.transfer(ctx, PhaseDownload, Conn.Download)
	if err != nil {
		return nil, err
	}
	up, err := r.transfer(ctx, PhaseUpload, Conn.Upload)
	if err != nil {
		return nil, err
	}
	return &Result{
		SchemaVersion: SchemaVersion,
		Timestamp:     r.cfg.Now().UTC().Truncate(time.Second),
		Server:        Server{Provider: meta.Provider, Colo: meta.Colo},
		Config:        RunConfig{DurationSeconds: r.cfg.Duration.Seconds(), Streams: r.cfg.Streams},
		Download:      down.result,
		Upload:        up.result,
		Latency: Latency{
			IdleMs:                   Round1(idleMs),
			JitterMs:                 Round1(jitterMs),
			LoadedDownloadMs:         round1Ptr(down.loaded),
			LoadedUploadMs:           round1Ptr(up.loaded),
			LoadedDownloadLowerBound: down.lowerBound,
			LoadedUploadLowerBound:   up.lowerBound,
		},
	}, nil
}

func round1Ptr(v *float64) *Dec1 {
	if v == nil {
		return nil
	}
	d := Round1(*v)
	return &d
}

func (r *runner) connect(ctx context.Context) (Metadata, error) {
	r.emit(PhaseStarted{PhaseConnect})
	cctx, cancel := context.WithTimeout(ctx, r.cfg.ConnectTimeout)
	defer cancel()
	meta, err := r.backend.Metadata(cctx)
	if err != nil {
		if ctx.Err() != nil {
			return meta, ctx.Err()
		}
		if errors.Is(err, ErrRejected) {
			return meta, err
		}
		return meta, fmt.Errorf("could not reach the test server: %w", err)
	}
	r.emit(Connected{Server{Provider: meta.Provider, Colo: meta.Colo}})
	return meta, nil
}

// withLastErr adds the most recent underlying error to err, if there is one.
func withLastErr(err, last error) error {
	if last == nil {
		return err
	}
	return fmt.Errorf("%w (last error: %v)", err, last)
}

// sleep waits for d, and reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
