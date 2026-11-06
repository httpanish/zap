package engine

import (
	"context"
	"errors"
	"sync"
	"time"
)

// prober sends latency probes during a transfer phase, each on a free connection
// from a pool of warm ones.
type prober struct {
	cfg    Config
	start  time.Time
	ramp   time.Duration
	free   chan Conn
	fatal  func(error)
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wg     sync.WaitGroup

	mu       sync.Mutex
	stopped  bool
	nextID   int
	inFlight map[int]time.Duration // Probe ID to the elapsed time it was sent.
	samples  []float64             // Probes sent after the ramp-up window, in ms.
	cutOff   int                   // How many samples were cut off at the deadline.

	once       sync.Once
	loaded     *float64
	lowerBound bool
}

func newProber(ctx context.Context, cfg Config, start time.Time, ramp time.Duration, pool []Conn, fatal func(error)) *prober {
	p := &prober{
		cfg:      cfg,
		start:    start,
		ramp:     ramp,
		free:     make(chan Conn, len(pool)),
		fatal:    fatal,
		done:     make(chan struct{}),
		inFlight: map[int]time.Duration{},
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	for _, c := range pool {
		p.free <- c
	}
	p.wg.Go(p.loop)
	return p
}

func (p *prober) loop() {
	t := time.NewTicker(p.cfg.ProbeInterval)
	defer t.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.done:
			return
		case <-t.C:
			p.fire()
		}
	}
}

func (p *prober) fire() {
	var conn Conn
	select {
	case conn = <-p.free:
	default:
		return // No connection is free, so this probe is skipped.
	}
	sentAt := p.cfg.Now().Sub(p.start)

	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	id := p.nextID
	p.nextID++
	p.inFlight[id] = sentAt
	p.mu.Unlock()

	p.wg.Go(func() {
		d, err := conn.Probe(p.ctx)
		if errors.Is(err, ErrRejected) {
			p.fatal(err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.stopped {
			return // Already counted as cut off.
		}
		delete(p.inFlight, id)
		if err == nil && sentAt >= p.ramp {
			p.samples = append(p.samples, toMs(d))
		}
		p.free <- conn
	})
}

// current returns the median so far, or nil if there are no samples yet.
func (p *prober) current() *float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.samples) == 0 {
		return nil
	}
	v := Median(p.samples)
	return &v
}

// stop ends probing at the given elapsed time. Probes still in flight count as
// samples using their elapsed time so far, because dropping them would flatter
// the worst links. Calling stop again returns the same figures.
func (p *prober) stop(at time.Duration) (loaded *float64, lowerBound bool) {
	p.once.Do(func() {
		p.mu.Lock()
		p.stopped = true
		var rampSent []float64
		for _, sentAt := range p.inFlight {
			if sentAt >= at {
				continue // Sent after the deadline, while stop was being called.
			}
			if sentAt >= p.ramp {
				p.samples = append(p.samples, toMs(at-sentAt))
				p.cutOff++
			} else {
				rampSent = append(rampSent, toMs(at-sentAt))
			}
		}
		// On long phases, probes sent during ramp-up can hold the whole pool until
		// the deadline. They are then the only evidence of a very slow link.
		if len(p.samples) == 0 && len(rampSent) > 0 {
			p.samples = rampSent
			p.cutOff = len(rampSent)
		}
		if len(p.samples) > 0 {
			v := Median(p.samples)
			p.loaded = &v
			p.lowerBound = p.cutOff*2 > len(p.samples)
		}
		p.mu.Unlock()

		close(p.done)
		p.cancel()
		p.wg.Wait()
	})
	return p.loaded, p.lowerBound
}
