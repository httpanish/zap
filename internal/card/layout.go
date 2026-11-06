// Package card renders a result as a shareable PNG.
package card

import (
	"fmt"
	"time"

	"github.com/httpanish/zap/internal/engine"
	"github.com/httpanish/zap/internal/output"
)

// Layout holds everything the card shows, already formatted. It only needs what
// history stores, so a card can later be redrawn from a past run.
type Layout struct {
	Download, Upload             string // In Mbps, without the unit.
	Idle, Jitter                 string // With the unit.
	LoadedDownload, LoadedUpload string // With the unit, or n/a.
	Date, Server                 string
	DownloadSeries, UploadSeries []float64
}

// NewLayout formats a result for the card, using loc for the date.
func NewLayout(r *engine.Result, loc *time.Location) Layout {
	return Layout{
		Download:       fmt.Sprintf("%.1f", float64(r.Download.Mbps)),
		Upload:         fmt.Sprintf("%.1f", float64(r.Upload.Mbps)),
		Idle:           fmt.Sprintf("%.1f ms", float64(r.Latency.IdleMs)),
		Jitter:         fmt.Sprintf("%.1f ms", float64(r.Latency.JitterMs)),
		LoadedDownload: loadedText(r.Latency.LoadedDownloadMs, r.Latency.LoadedDownloadLowerBound),
		LoadedUpload:   loadedText(r.Latency.LoadedUploadMs, r.Latency.LoadedUploadLowerBound),
		Date:           r.Timestamp.In(loc).Format("2 Jan 2006, 15:04"),
		Server:         output.ProviderName(r.Server.Provider) + " · " + r.Server.Colo,
		DownloadSeries: r.Download.Samples(),
		UploadSeries:   r.Upload.Samples(),
	}
}

func loadedText(v *engine.Dec1, lowerBound bool) string {
	number := output.LoadedNumber(v, lowerBound)
	if number == output.NotAvailable {
		return number
	}
	return number + " ms"
}
