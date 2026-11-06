package cloudflare

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Config{Version: "test", BaseURL: srv.URL, TransferBytes: 100_000})
}

func noBytes(int) {}

func TestRequestHeaders(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()
	})
	ctx := context.Background()
	conn := c.NewConn()
	defer conn.Close()
	if _, err := c.Metadata(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if err := conn.Download(ctx, noBytes); err != nil {
		t.Fatal(err)
	}
	if err := conn.Upload(ctx, noBytes); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d requests, want 4", len(seen))
	}
	for _, r := range seen {
		if got := r.Header.Get("Accept-Encoding"); got != "" {
			t.Errorf("%s: Accept-Encoding = %q, want none", r.URL, got)
		}
		if got := r.Header.Get("User-Agent"); got != "zap/test" {
			t.Errorf("%s: User-Agent = %q", r.URL, got)
		}
		if r.ProtoMajor != 1 {
			t.Errorf("%s: protocol %s, want HTTP/1.x", r.URL, r.Proto)
		}
	}
}

func TestProxyWarning(t *testing.T) {
	proxy, _ := url.Parse("http://alice:s3cret@proxy.example:8080")
	c := New(Config{Version: "test", Proxy: func(*http.Request) (*url.URL, error) { return proxy, nil }})
	got := c.ProxyWarning()
	if want := "Using proxy proxy.example:8080: results measure the proxy path"; got != want {
		t.Errorf("ProxyWarning() = %q, want %q", got, want)
	}
	if strings.Contains(got, "alice") || strings.Contains(got, "s3cret") {
		t.Errorf("warning leaks credentials: %q", got)
	}
}

func TestNoProxyNoWarning(t *testing.T) {
	c := New(Config{Version: "test", Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }})
	if got := c.ProxyWarning(); got != "" {
		t.Errorf("ProxyWarning() = %q, want none", got)
	}
}

func TestMetadata(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"with colo", "ip=203.0.113.9\ncolo=LHR\n", "LHR"},
		{"without colo", "ip=203.0.113.9\n", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/cdn-cgi/trace" {
					t.Errorf("path = %s", r.URL.Path)
				}
				io.WriteString(w, tt.body)
			})
			meta, err := c.Metadata(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if meta.Colo != tt.want || meta.Provider != "cloudflare" {
				t.Errorf("Metadata() = %+v, want colo %q", meta, tt.want)
			}
		})
	}
}

func TestRejectionsOnEveryRequestType(t *testing.T) {
	requests := map[string]func(*Client, engine.Conn) error{
		"metadata": func(c *Client, _ engine.Conn) error {
			_, err := c.Metadata(context.Background())
			return err
		},
		"probe": func(_ *Client, conn engine.Conn) error {
			_, err := conn.Probe(context.Background())
			return err
		},
		"download": func(_ *Client, conn engine.Conn) error { return conn.Download(context.Background(), noBytes) },
		"upload":   func(_ *Client, conn engine.Conn) error { return conn.Upload(context.Background(), noBytes) },
	}
	for _, status := range []int{403, 429} {
		for name, do := range requests {
			t.Run(name+"/"+strconv.Itoa(status), func(t *testing.T) {
				c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
				conn := c.NewConn()
				defer conn.Close()
				err := do(c, conn)
				if !errors.Is(err, engine.ErrRejected) {
					t.Fatalf("err = %v, want a rejection", err)
				}
				if !strings.Contains(err.Error(), "Cloudflare rejected the test") {
					t.Errorf("message = %q", err)
				}
			})
		}
	}
}

func TestRejectionReportsRetryAfter(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3229")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	conn := c.NewConn()
	defer conn.Close()
	err := conn.Download(context.Background(), noBytes)
	if !errors.Is(err, engine.ErrRejected) {
		t.Fatalf("err = %v, want a rejection", err)
	}
	if !strings.Contains(err.Error(), "try again in about 54 minutes") {
		t.Errorf("message = %q", err)
	}
}

func TestServerErrorIsNotARejection(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	conn := c.NewConn()
	defer conn.Close()
	err := conn.Download(context.Background(), noBytes)
	if err == nil || errors.Is(err, engine.ErrRejected) {
		t.Errorf("err = %v, want a non-fatal error", err)
	}
}

func TestProbeSubtractsServerTime(t *testing.T) {
	const delay = 60 * time.Millisecond
	tests := []struct {
		name   string
		timing string
		check  func(time.Duration) bool
	}{
		{"missing fields subtract nothing", "", func(d time.Duration) bool { return d >= delay }},
		{"fields are subtracted", "cfSpeedEdge;dur=20, cfSpeedWorker;dur=30", func(d time.Duration) bool { return d >= 10*time.Millisecond && d < delay }},
		{"floored at zero", "cfSpeedWorker;dur=100000", func(d time.Duration) bool { return d == 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(delay)
				if tt.timing != "" {
					w.Header().Set("Server-Timing", tt.timing)
				}
			})
			conn := c.NewConn()
			defer conn.Close()
			got, err := conn.Probe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !tt.check(got) {
				t.Errorf("sample = %v", got)
			}
		})
	}
}

func TestProbeRequestsNoPayload(t *testing.T) {
	var query atomic.Value
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { query.Store(r.URL.RequestURI()) })
	conn := c.NewConn()
	defer conn.Close()
	if _, err := conn.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := query.Load(); got != "/__down?bytes=0" {
		t.Errorf("request = %v", got)
	}
}

func TestDownloadCountsPayloadBytes(t *testing.T) {
	var uri atomic.Value
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		uri.Store(r.URL.RequestURI())
		w.Write(make([]byte, 100_000))
	})
	conn := c.NewConn()
	defer conn.Close()
	var total int
	if err := conn.Download(context.Background(), func(n int) { total += n }); err != nil {
		t.Fatal(err)
	}
	if total != 100_000 {
		t.Errorf("counted %d bytes, want 100000", total)
	}
	if got := uri.Load(); got != "/__down?bytes=100000" {
		t.Errorf("request = %v", got)
	}
}

func TestUploadCountsInSmallChunks(t *testing.T) {
	var received atomic.Int64
	var contentLength atomic.Int64
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		contentLength.Store(r.ContentLength)
		n, _ := io.Copy(io.Discard, r.Body)
		received.Store(n)
	})
	conn := c.NewConn()
	defer conn.Close()
	var total, largest atomic.Int64
	err := conn.Upload(context.Background(), func(n int) {
		total.Add(int64(n))
		if int64(n) > largest.Load() {
			largest.Store(int64(n))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if total.Load() != 100_000 || received.Load() != 100_000 || contentLength.Load() != 100_000 {
		t.Errorf("counted %d, server received %d, Content-Length %d, want 100000", total.Load(), received.Load(), contentLength.Load())
	}
	if largest.Load() > uploadChunkBytes {
		t.Errorf("largest chunk = %d, want at most %d", largest.Load(), uploadChunkBytes)
	}
}

func TestUploadBodyWrapsTheSharedBuffer(t *testing.T) {
	data := []byte{1, 2, 3}
	var counted int
	body := &uploadBody{data: data, remaining: 7, onBytes: func(n int) { counted += n }}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 2, 3, 1, 2, 3, 1}; string(got) != string(want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	if counted != 7 {
		t.Errorf("counted = %d, want 7", counted)
	}
}

func TestEachConnUsesOneConnection(t *testing.T) {
	var mu sync.Mutex
	addrs := map[string]bool{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			addrs[c.RemoteAddr().String()] = true
			mu.Unlock()
		}
	}
	srv.Start()
	defer srv.Close()
	c := New(Config{Version: "test", BaseURL: srv.URL})

	connA, connB := c.NewConn(), c.NewConn()
	defer connA.Close()
	defer connB.Close()
	for range 3 {
		for _, conn := range []engine.Conn{connA, connB} {
			if _, err := conn.Probe(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 2 {
		t.Errorf("server saw %d connections, want 2", len(addrs))
	}
}
