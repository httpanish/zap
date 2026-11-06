package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
	"time"
)

// uploadChunkBytes is how much an upload counts at a time. Larger chunks could
// look like a stall on a slow working link.
const uploadChunkBytes = 16 << 10

type conn struct {
	client    *Client
	transport *http.Transport
	buf       []byte
}

// Probe returns the time to first byte of a minimal request, minus the server's
// reported processing time, floored at 0. The clock starts once a connection is
// in hand, so a redial adds no setup cost to the sample.
func (c *conn) Probe(ctx context.Context) (time.Duration, error) {
	base := time.Now()
	var gotConn, firstByte atomic.Int64
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn:              func(httptrace.GotConnInfo) { gotConn.Store(int64(time.Since(base))) },
		GotFirstResponseByte: func() { firstByte.Store(int64(time.Since(base))) },
	})
	req, err := c.client.newRequest(ctx, http.MethodGet, downPath+"?bytes=0", nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.transport.RoundTrip(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return 0, err
	}
	// Reading to the end lets the connection be reused for the next probe.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return 0, err
	}
	sample := time.Duration(firstByte.Load()-gotConn.Load()) - serverTime(resp.Header.Values("Server-Timing"))
	return max(sample, 0), nil
}

// Download makes one fixed-size request, reporting bytes as they arrive.
func (c *conn) Download(ctx context.Context, onBytes func(int)) error {
	path := fmt.Sprintf("%s?bytes=%d", downPath, c.client.cfg.TransferBytes)
	req, err := c.client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := c.transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	for {
		n, err := resp.Body.Read(c.buf)
		onBytes(n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Upload makes one fixed-size request, reporting bytes as they are read from the
// request body. Socket buffering means this can run ahead of the network.
func (c *conn) Upload(ctx context.Context, onBytes func(int)) error {
	body := &uploadBody{data: c.client.uploadData, remaining: c.client.cfg.TransferBytes, onBytes: onBytes}
	req, err := c.client.newRequest(ctx, http.MethodPost, upPath, body)
	if err != nil {
		return err
	}
	req.ContentLength = c.client.cfg.TransferBytes
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// Close releases the connection.
func (c *conn) Close() { c.transport.CloseIdleConnections() }

// uploadBody repeats a shared buffer of random bytes for the length of a request.
type uploadBody struct {
	data      []byte
	offset    int
	remaining int64
	onBytes   func(int)
}

func (b *uploadBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), uploadChunkBytes, len(b.data)-b.offset)
	if int64(n) > b.remaining {
		n = int(b.remaining)
	}
	copy(p[:n], b.data[b.offset:])
	b.offset = (b.offset + n) % len(b.data)
	b.remaining -= int64(n)
	b.onBytes(n)
	return n, nil
}
