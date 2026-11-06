//go:build unix

package main

import (
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

func TestSignalsExitWithTheirCodeAndSaveNothing(t *testing.T) {
	tests := []struct {
		name   string
		signal syscall.Signal
		want   int
	}{
		{"SIGINT", syscall.SIGINT, exitInterrupt},
		{"SIGTERM", syscall.SIGTERM, exitTerminate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.app.tuneEngine = func(c *engine.Config) { c.Duration = time.Minute }

			var sent atomic.Bool
			time.AfterFunc(200*time.Millisecond, func() {
				sent.Store(true)
				syscall.Kill(syscall.Getpid(), tt.signal)
			})
			begin := time.Now()
			code := h.run("--json")
			if !sent.Load() {
				t.Fatalf("the run ended before the signal, with exit %d: %s", code, h.stderr.String())
			}
			if code != tt.want {
				t.Errorf("exit = %d, want %d", code, tt.want)
			}
			if took := time.Since(begin); took > 5*time.Second {
				t.Errorf("exiting took %v", took)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", h.stdout.String())
			}
			if strings.Contains(h.stderr.String(), "context canceled") {
				t.Errorf("stderr shows an internal error: %q", h.stderr.String())
			}
			if h.savedRuns() != 0 {
				t.Error("an interrupted run was saved")
			}
		})
	}
}
