package cloudflare

import (
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
