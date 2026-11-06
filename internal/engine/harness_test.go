package engine_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/cloudflare"
	"github.com/httpanish/zap/internal/engine"
)

// fakeCloudflare is a local server with a hook for each request type. A nil hook
// gets a healthy default response.
type fakeCloudflare struct {
	trace, probe, down, up http.HandlerFunc
	release                chan struct{} // Closed at cleanup, to free blocked handlers.
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var hook http.HandlerFunc
	switch {
	case r.URL.Path == "/cdn-cgi/trace":
		hook = f.trace
		if hook == nil {
			hook = func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "colo=TST\n") }
		}
	case r.URL.Path == "/__down" && r.URL.Query().Get("bytes") == "0":
		hook = f.probe
		if hook == nil {
			hook = func(http.ResponseWriter, *http.Request) {}
		}
	case r.URL.Path == "/__down":
		hook = f.down
		if hook == nil {
			hook = func(w http.ResponseWriter, r *http.Request) { f.stream(w, r, 0) }
		}
	case r.URL.Path == "/__up":
		hook = f.up
		if hook == nil {
			hook = func(_ http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body) }
		}
	default:
		hook = http.NotFound
	}
	hook(w, r)
}

// stream writes small chunks until the client goes away. If stopAfter is set, it
// stalls from that point on.
func (f *fakeCloudflare) stream(w http.ResponseWriter, r *http.Request, stopAfter time.Duration) {
	chunk := make([]byte, 16<<10)
	begin := time.Now()
	for stopAfter == 0 || time.Since(begin) < stopAfter {
		if _, err := w.Write(chunk); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-f.release:
			return
		case <-time.After(2 * time.Millisecond):
		}
	}
	f.block(r)
}

// block holds a request open until the client goes away or the test ends.
func (f *fakeCloudflare) block(r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-f.release:
	}
}

// start serves f and returns a backend for it.
func (f *fakeCloudflare) start(t *testing.T) *cloudflare.Client {
	t.Helper()
	f.release = make(chan struct{})
	srv := httptest.NewServer(f)
	t.Cleanup(func() {
		close(f.release)
		srv.CloseClientConnections()
		srv.Close()
	})
	return cloudflare.New(cloudflare.Config{Version: "test", BaseURL: srv.URL, TransferBytes: 200_000})
}

// fastConfig shrinks the run to 200 ms phases, with the limits in proportion.
func fastConfig() engine.Config {
	return engine.Config{
		Duration:           200 * time.Millisecond,
		Streams:            2,
		StallLimit:         150 * time.Millisecond,
		FirstByteAllowance: 500 * time.Millisecond,
		ConnectTimeout:     500 * time.Millisecond,
		SampleInterval:     10 * time.Millisecond,
		ProbeInterval:      10 * time.Millisecond,
		ProbePoolSize:      4,
		PoolWarmTimeout:    100 * time.Millisecond,
		RetryDelay:         5 * time.Millisecond,
	}
}

// run drives a test to completion and returns its outcome.
func run(ctx context.Context, cfg engine.Config, b engine.Backend) engine.Finished {
	var finished engine.Finished
	for ev := range engine.Start(ctx, cfg, b) {
		if f, ok := ev.(engine.Finished); ok {
			finished = f
		}
	}
	return finished
}
