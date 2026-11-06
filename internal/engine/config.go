package engine

import (
	"context"
	"errors"
	"time"
)

// ErrRejected marks a response that must end the run immediately, such as a 403
// or 429. Backends wrap it in an error describing the rejection.
var ErrRejected = errors.New("test rejected by the server")

// Metadata describes the server a test runs against.
type Metadata struct {
	Provider string
	Colo     string
}

// Conn is one dedicated connection to the backend. Its methods are called by one
// goroutine at a time.
type Conn interface {
	// Probe makes a minimal request and returns its latency sample.
	Probe(ctx context.Context) (time.Duration, error)
	// Download makes one download request, reporting payload bytes as they arrive.
	Download(ctx context.Context, onBytes func(int)) error
	// Upload makes one upload request, reporting payload bytes as they are read.
	Upload(ctx context.Context, onBytes func(int)) error
	// Close releases the connection.
	Close()
}

// Backend is the service under test.
type Backend interface {
	Metadata(ctx context.Context) (Metadata, error)
	NewConn() Conn
}

// Config controls a run. Zero values take the defaults from the specification.
// Range limits on Duration and Streams are enforced by the CLI, not here.
type Config struct {
	Duration time.Duration // Length of each transfer phase. Default 10s.
	Streams  int           // Parallel connections per transfer phase. Default 4.

	Now                func() time.Time // Clock. Default time.Now.
	StallLimit         time.Duration    // Default 3s.
	FirstByteAllowance time.Duration    // Time allowed before a phase's first byte. Default 10s.
	ConnectTimeout     time.Duration    // Default 10s.
	SampleInterval     time.Duration    // Default 100ms.
	ProbeInterval      time.Duration    // Default 250ms.
	ProbePoolSize      int              // Default 8.
	PoolWarmTimeout    time.Duration    // Default 3s.
	IdleAttempts       int              // Default 20.
	MinIdleSamples     int              // Default 5.
	RetryDelay         time.Duration    // Default 250ms.
}

func (c Config) withDefaults() Config {
	setDuration := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	setInt := func(v *int, d int) {
		if *v == 0 {
			*v = d
		}
	}
	setDuration(&c.Duration, 10*time.Second)
	setDuration(&c.StallLimit, 3*time.Second)
	setDuration(&c.FirstByteAllowance, 10*time.Second)
	setDuration(&c.ConnectTimeout, 10*time.Second)
	setDuration(&c.SampleInterval, 100*time.Millisecond)
	setDuration(&c.ProbeInterval, 250*time.Millisecond)
	setDuration(&c.PoolWarmTimeout, 3*time.Second)
	setDuration(&c.RetryDelay, 250*time.Millisecond)
	setInt(&c.Streams, 4)
	setInt(&c.ProbePoolSize, 8)
	setInt(&c.IdleAttempts, 20)
	setInt(&c.MinIdleSamples, 5)
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}
