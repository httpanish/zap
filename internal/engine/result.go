package engine

import (
	"math"
	"strconv"
	"time"
)

// SchemaVersion is the version of the result object written to JSON and history.
const SchemaVersion = 1

// Dec1 is a value rounded to one decimal place. It always marshals with exactly
// one decimal, so 12 appears as 12.0 in JSON.
type Dec1 float64

// MarshalJSON implements json.Marshaler.
func (d Dec1) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, float64(d), 'f', 1, 64), nil
}

// Round1 rounds v to one decimal place.
func Round1(v float64) Dec1 {
	return Dec1(math.Round(v*10) / 10)
}

// Server identifies where the test ran.
type Server struct {
	Provider string `json:"provider"`
	Colo     string `json:"colo"`
}

// RunConfig records the settings a run used.
type RunConfig struct {
	DurationSeconds float64 `json:"duration_seconds"`
	Streams         int     `json:"streams"`
}

// PhaseResult holds the figures for one transfer phase.
type PhaseResult struct {
	Mbps        Dec1   `json:"mbps"`
	Bytes       int64  `json:"bytes"`
	SamplesMbps []Dec1 `json:"samples_mbps"`
}

// Latency holds the latency figures for a run.
type Latency struct {
	IdleMs           Dec1  `json:"idle_ms"`
	JitterMs         Dec1  `json:"jitter_ms"`
	LoadedDownloadMs *Dec1 `json:"loaded_download_ms"`
	LoadedUploadMs   *Dec1 `json:"loaded_upload_ms"`

	// True when more than half the samples were cut off at the deadline. These
	// are display-only and not in the JSON schema, so runs read back from history
	// don't have them.
	LoadedDownloadLowerBound bool `json:"-"`
	LoadedUploadLowerBound   bool `json:"-"`
}

// Result is the object used for --json output and for each history line.
type Result struct {
	SchemaVersion int         `json:"schema_version"`
	Timestamp     time.Time   `json:"timestamp"`
	Server        Server      `json:"server"`
	Config        RunConfig   `json:"config"`
	Download      PhaseResult `json:"download"`
	Upload        PhaseResult `json:"upload"`
	Latency       Latency     `json:"latency"`
}
