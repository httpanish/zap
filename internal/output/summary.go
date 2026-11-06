// Package output renders results as plain text and JSON.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/httpanish/zap/internal/engine"
)

// NotAvailable is shown for a loaded latency with no usable samples.
const NotAvailable = "n/a"

// LoadedNumber formats a loaded latency without its unit. A figure that is only a
// lower bound is marked with >=.
func LoadedNumber(v *engine.Dec1, lowerBound bool) string {
	switch {
	case v == nil:
		return NotAvailable
	case lowerBound:
		return fmt.Sprintf(">=%.1f", float64(*v))
	}
	return fmt.Sprintf("%.1f", float64(*v))
}

// ProviderName returns the display name of a run's provider.
func ProviderName(provider string) string {
	if provider == "" {
		return ""
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

// Summary writes the plain text summary, with the numbers right-aligned.
func Summary(w io.Writer, r *engine.Result) error {
	down := fmt.Sprintf("%.1f", float64(r.Download.Mbps))
	up := fmt.Sprintf("%.1f", float64(r.Upload.Mbps))
	idle := fmt.Sprintf("%.1f", float64(r.Latency.IdleMs))
	jitter := fmt.Sprintf("%.1f", float64(r.Latency.JitterMs))
	loadedDown := LoadedNumber(r.Latency.LoadedDownloadMs, r.Latency.LoadedDownloadLowerBound)
	loadedUp := LoadedNumber(r.Latency.LoadedUploadMs, r.Latency.LoadedUploadLowerBound)
	data := fmt.Sprintf("%.1f", float64(r.DataUsedBytes())/1e6)

	width := 0
	for _, s := range []string{down, up, idle, loadedDown, data} {
		width = max(width, len(s))
	}
	_, err := fmt.Fprintf(w,
		"Download   %[1]*[2]s Mbps\n"+
			"Upload     %[1]*[3]s Mbps\n"+
			"Latency    %[1]*[4]s ms (jitter %[5]s ms)\n"+
			"Loaded     %[1]*[6]s%[7]s down, %[8]s%[9]s up\n"+
			"Server      %[10]s %[11]s\n"+
			"Data used  %[1]*[12]s MB\n",
		width, down, up, idle, jitter,
		loadedDown, msUnit(loadedDown), loadedUp, msUnit(loadedUp),
		ProviderName(r.Server.Provider), r.Server.Colo, data)
	return err
}

func msUnit(number string) string {
	if number == NotAvailable {
		return ""
	}
	return " ms"
}

// JSON writes v as indented JSON with a trailing newline.
func JSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
