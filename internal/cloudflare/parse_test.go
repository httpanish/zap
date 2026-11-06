package cloudflare

import (
	"testing"
	"time"
)

func TestParseColo(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"present", "fl=1\nip=203.0.113.9\ncolo=LHR\ntls=TLSv1.3\n", "LHR"},
		{"no trailing newline", "colo=BOM", "BOM"},
		{"missing", "fl=1\nip=203.0.113.9\n", unknownColo},
		{"empty value", "colo=\n", unknownColo},
		{"empty body", "", unknownColo},
		{"only matches the whole key", "notcolo=XXX\n", unknownColo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseColo(tt.body); got != tt.want {
				t.Errorf("parseColo() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServerTime(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		want    time.Duration
	}{
		{"no header", nil, 0},
		{"both fields", []string{"cfSpeedEdge;dur=2, cfSpeedWorker;dur=15.5"}, 17500 * time.Microsecond},
		{"only one field", []string{"cfSpeedWorker;dur=15"}, 15 * time.Millisecond},
		{"ignores other fields", []string{"cfL4;rtt=9000, cfSpeedEdge;dur=2, cdn-cache;dur=99"}, 2 * time.Millisecond},
		{"separate header lines", []string{"cfSpeedEdge;dur=2", "cfSpeedWorker;dur=3"}, 5 * time.Millisecond},
		{"field without a duration", []string{"cfSpeedEdge;rtt=4"}, 0},
		{"malformed duration", []string{"cfSpeedEdge;dur=abc, cfSpeedWorker;dur=3"}, 3 * time.Millisecond},
		{"negative duration", []string{"cfSpeedEdge;dur=-5"}, 0},
		{"names are case-insensitive", []string{"CFSPEEDEDGE;dur=1"}, time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serverTime(tt.headers); got != tt.want {
				t.Errorf("serverTime() = %v, want %v", got, tt.want)
			}
		})
	}
}
