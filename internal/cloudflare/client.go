// Package cloudflare is the client for Cloudflare's speed test endpoints. All
// knowledge of those endpoints stays in this package. They aren't a supported
// public API, and may change or be rate limited.
package cloudflare

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

const (
	defaultBaseURL = "https://speed.cloudflare.com"
	tracePath      = "/cdn-cgi/trace"
	downPath       = "/__down"
	upPath         = "/__up"

	// defaultTransferBytes is confirmed by the endpoint check. Downloads of 100 MB
	// or more are rejected with a 403, which would be fatal.
	defaultTransferBytes = 25_000_000

	uploadBufferBytes = 1 << 20
	maxTraceBytes     = 64 << 10
	ioBufferBytes     = 64 << 10
)

// Config sets up a Client. Only Version is needed outside tests.
type Config struct {
	Version       string
	BaseURL       string
	TransferBytes int64
	Proxy         func(*http.Request) (*url.URL, error)
}

// Client implements engine.Backend.
type Client struct {
	cfg        Config
	uploadData []byte // Shared by every upload, so generating data can't limit speed.
}

// New returns a Client.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.TransferBytes == 0 {
		cfg.TransferBytes = defaultTransferBytes
	}
	if cfg.Proxy == nil {
		cfg.Proxy = http.ProxyFromEnvironment
	}
	data := make([]byte, uploadBufferBytes)
	rand.Read(data) // Never fails: see the crypto/rand documentation.
	return &Client{cfg: cfg, uploadData: data}
}

// ProxyWarning returns the warning to show if a proxy is in use, or an empty
// string if not. It names only the host, so credentials can't leak.
func (c *Client) ProxyWarning() string {
	req, err := http.NewRequest(http.MethodGet, c.cfg.BaseURL+tracePath, nil)
	if err != nil {
		return ""
	}
	proxy, err := c.cfg.Proxy(req)
	if err != nil || proxy == nil {
		return ""
	}
	return fmt.Sprintf("Using proxy %s: results measure the proxy path", proxy.Host)
}

// newTransport returns a transport for one connection, pinned to HTTP/1.1 so that
// each stream has its own TCP connection and no HTTP/2 flow-control window.
func (c *Client) newTransport() *http.Transport {
	return &http.Transport{
		Proxy:               c.cfg.Proxy,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12},
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		DisableCompression:  true, // Transparent gzip would distort byte counts.
		MaxIdleConnsPerHost: 1,
		ReadBufferSize:      ioBufferBytes,
		WriteBufferSize:     uploadChunkBytes,
	}
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zap/"+c.cfg.Version)
	return req, nil
}

// Metadata fetches the data centre. It doubles as the reachability check.
func (c *Client) Metadata(ctx context.Context) (engine.Metadata, error) {
	meta := engine.Metadata{Provider: "cloudflare", Colo: unknownColo}
	req, err := c.newRequest(ctx, http.MethodGet, tracePath, nil)
	if err != nil {
		return meta, err
	}
	transport := c.newTransport()
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return meta, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return meta, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTraceBytes))
	if err != nil {
		return meta, err
	}
	meta.Colo = parseColo(string(body))
	return meta, nil
}

// NewConn returns a connection with its own transport.
func (c *Client) NewConn() engine.Conn {
	return &conn{client: c, transport: c.newTransport(), buf: make([]byte, ioBufferBytes)}
}

// RejectedError is an HTTP 403 or 429, which is fatal for the whole run.
type RejectedError struct {
	Status     int
	RetryAfter time.Duration // Zero if the response didn't say.
}

func (e *RejectedError) Error() string {
	msg := fmt.Sprintf("Cloudflare rejected the test (HTTP %d)", e.Status)
	if e.RetryAfter > 0 {
		msg += ", try again in about " + roughDuration(e.RetryAfter)
	}
	return msg
}

// Unwrap lets the engine recognise a rejection without knowing this package.
func (e *RejectedError) Unwrap() error { return engine.ErrRejected }

func checkStatus(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return &RejectedError{
			Status:     resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	return nil
}
