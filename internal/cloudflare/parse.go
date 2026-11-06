package cloudflare

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// unknownColo is reported when the metadata response has no data centre.
const unknownColo = "unknown"

// parseColo returns the colo field from a /cdn-cgi/trace body. It reads nothing
// else, so the public IP address in that body is never kept.
func parseColo(body string) string {
	for line := range strings.Lines(body) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "colo="); ok && v != "" {
			return v
		}
	}
	return unknownColo
}

// serverTime sums the cfSpeedEdge and cfSpeedWorker durations in Server-Timing
// header values. These names are undocumented, and only this package reads them.
// Anything missing or malformed counts as zero.
func serverTime(headers []string) time.Duration {
	var total time.Duration
	for _, header := range headers {
		for entry := range strings.SplitSeq(header, ",") {
			name, params, _ := strings.Cut(strings.TrimSpace(entry), ";")
			if !strings.EqualFold(name, "cfSpeedEdge") && !strings.EqualFold(name, "cfSpeedWorker") {
				continue
			}
			total += durParam(params)
		}
	}
	return total
}

// parseRetryAfter reads a Retry-After header, which is either a number of seconds
// or an HTTP date. Anything missing, malformed, or in the past gives zero.
func parseRetryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	when, err := http.ParseTime(header)
	if err != nil {
		return 0
	}
	return max(when.Sub(now), 0)
}

// roughDuration describes a wait in the largest sensible unit, rounding up so
// that waiting as long as it says is enough.
func roughDuration(d time.Duration) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d < time.Minute:
		return plural(int(math.Ceil(d.Seconds())), "second")
	case d < 2*time.Hour:
		return plural(int(math.Ceil(d.Minutes())), "minute")
	}
	return plural(int(math.Ceil(d.Hours())), "hour")
}

func durParam(params string) time.Duration {
	for param := range strings.SplitSeq(params, ";") {
		v, ok := strings.CutPrefix(strings.TrimSpace(param), "dur=")
		if !ok {
			continue
		}
		ms, err := strconv.ParseFloat(v, 64)
		if err != nil || ms < 0 {
			return 0
		}
		return time.Duration(ms * float64(time.Millisecond))
	}
	return 0
}
