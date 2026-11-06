package output

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

// trendOverhead is the width of the trend line without its two sparklines.
var trendOverhead = len("Download   Upload   (oldest > newest)")

// HistoryTable writes the trend sparklines and the table. Runs are newest first,
// so the sparklines reverse them to read oldest to newest. Width is the terminal
// width that the trend line has to fit.
func HistoryTable(w io.Writer, runs []engine.Result, width int, loc *time.Location) error {
	var down, up []float64
	for _, r := range slices.Backward(runs) {
		down = append(down, float64(r.Download.Mbps))
		up = append(up, float64(r.Upload.Mbps))
	}
	spark := max((width-trendOverhead)/2, 1)

	rows := [][]string{{"DATE", "DOWN", "UP", "PING", "SERVER"}}
	for _, r := range runs {
		rows = append(rows, []string{
			r.Timestamp.In(loc).Format("2006-01-02 15:04"),
			fmt.Sprintf("%.1f Mbps", float64(r.Download.Mbps)),
			fmt.Sprintf("%.1f Mbps", float64(r.Upload.Mbps)),
			fmt.Sprintf("%.0f ms", float64(r.Latency.IdleMs)),
			r.Server.Colo,
		})
	}
	// Widths come from the content, but start at those of typical values, so that
	// columns line up between runs of the command.
	widths := []int{16, 10, 9, 6}
	for _, row := range rows {
		for i := range widths {
			widths[i] = max(widths[i], len(row[i]))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Download %s  Upload %s  (oldest → newest)\n\n", Sparkline(down, spark), Sparkline(up, spark))
	for _, row := range rows {
		for i, cell := range row[:len(widths)] {
			fmt.Fprintf(&b, "%-*s  ", widths[i], cell)
		}
		b.WriteString(row[len(widths)] + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
