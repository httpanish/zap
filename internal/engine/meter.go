package engine

import (
	"sync/atomic"
	"time"
)

// meter counts payload bytes on the engine's clock. Bytes after the deadline are
// left out of the total, but still count as signs of life for stall detection.
type meter struct {
	now      func() time.Time
	start    time.Time
	duration time.Duration
	total    atomic.Int64
	lastByte atomic.Int64 // Elapsed nanoseconds at the last byte, plus 1. Zero means never.
}

func (m *meter) add(n int) {
	if n <= 0 {
		return
	}
	el := m.now().Sub(m.start)
	if el < m.duration {
		m.total.Add(int64(n))
	}
	m.lastByte.Store(int64(el) + 1)
}

// last returns when the most recent byte arrived, and whether any has.
func (m *meter) last() (time.Duration, bool) {
	v := m.lastByte.Load()
	return time.Duration(v - 1), v != 0
}
