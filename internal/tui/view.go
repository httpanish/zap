package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/httpanish/zap/internal/engine"
	"github.com/httpanish/zap/internal/output"
)

const (
	minWidth      = 40 // Below this, or minHeight, the view is one compact line.
	minHeight     = 11
	secondaryFrom = 60 // Below this width, secondary text is dropped.
	barWidth      = 20
	labelWidth    = 10
)

var (
	labelStyle    = lipgloss.NewStyle().Bold(true)
	mutedStyle    = lipgloss.NewStyle().Faint(true)
	downloadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#3987e5"))
	uploadStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#d95926"))
)

// View implements tea.Model.
func (m Model) View() string {
	if m.finished || m.cancelled {
		return "" // The alternate screen is closing, and the summary follows it.
	}
	if m.width < minWidth || m.height < minHeight {
		return truncate(m.compact(), m.width)
	}
	lines := []string{
		m.split(" "+labelStyle.Render("zap"), m.serverText()),
		"",
		m.pingLine(),
		"",
	}
	lines = append(lines, m.transferLines("DOWNLOAD", m.download, downloadStyle)...)
	lines = append(lines, "")
	lines = append(lines, m.transferLines("UPLOAD", m.upload, uploadStyle)...)
	lines = append(lines, "", m.split(" "+mutedStyle.Render("q quit"), m.dataText()))
	return strings.Join(lines, "\n")
}

// compact is the single line shown in very small terminals.
func (m Model) compact() string {
	switch m.phase {
	case engine.PhaseConnect:
		return "connecting..."
	case engine.PhaseIdle:
		return fmt.Sprintf("latency %s", percent(m.idleFraction))
	case engine.PhaseDownload:
		return fmt.Sprintf("download %s  %s", percent(m.download.fraction), speed(m.download.mbps))
	default:
		return fmt.Sprintf("upload %s  %s", percent(m.upload.fraction), speed(m.upload.mbps))
	}
}

func (m Model) showSecondary() bool { return m.width >= secondaryFrom }

func (m Model) serverText() string {
	if !m.showSecondary() || m.colo == "" {
		return ""
	}
	return "Cloudflare · " + m.colo
}

func (m Model) dataText() string {
	if !m.showSecondary() {
		return ""
	}
	return fmt.Sprintf("data used %.1f MB", float64(m.totalBytes)/1e6)
}

func (m Model) pingLine() string {
	label := labelStyle.Render(fmt.Sprintf("%-*s", labelWidth, "PING"))
	switch {
	case m.idleDone:
		line := fmt.Sprintf(" %s%.0f ms", label, m.idleMs)
		if m.showSecondary() {
			line += mutedStyle.Render(fmt.Sprintf("   jitter %.0f ms", m.jitterMs))
		}
		return line
	case m.phase == engine.PhaseIdle:
		return " " + label + "measuring..."
	}
	return " " + label + mutedStyle.Render("waiting...")
}

func (m Model) transferLines(name string, t transfer, accent lipgloss.Style) []string {
	label := labelStyle.Render(fmt.Sprintf("%-*s", labelWidth, name))
	if !t.started {
		return []string{" " + label + mutedStyle.Render("waiting...")}
	}
	sparkWidth := m.width - labelWidth - 2
	shown := min(len(t.series), int(math.Ceil(float64(sparkWidth)*t.fraction)))
	spark := accent.Render(output.Sparkline(t.series, shown))

	filled := min(int(math.Round(t.fraction*barWidth)), barWidth)
	bar := accent.Render(strings.Repeat("█", filled)) + mutedStyle.Render(strings.Repeat("░", barWidth-filled))
	return []string{
		" " + label + spark,
		m.split(fmt.Sprintf(" %s  %s", bar, speed(t.mbps)), m.loadedText(t)),
	}
}

// loadedText describes latency under load, as the increase over idle.
func (m Model) loadedText(t transfer) string {
	if !m.showSecondary() {
		return ""
	}
	if t.loaded == nil {
		if t.done {
			return "loaded " + output.NotAvailable
		}
		return ""
	}
	prefix := ""
	if t.lowerBound {
		prefix = ">="
	}
	return fmt.Sprintf("loaded %s%.0f ms (+%.0f)", prefix, *t.loaded, max(*t.loaded-m.idleMs, 0))
}

// split puts left and right on one line, with right against the margin. Right is
// dropped if the two don't fit.
func (m Model) split(left, right string) string {
	if right == "" {
		return left
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + mutedStyle.Render(right)
}

func speed(mbps float64) string {
	if mbps < 10 {
		return fmt.Sprintf("%.1f Mbps", mbps)
	}
	return fmt.Sprintf("%.0f Mbps", mbps)
}

func percent(fraction float64) string {
	return fmt.Sprintf("%.0f%%", fraction*100)
}

func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		runes = runes[:max(width, 0)]
	}
	return string(runes)
}
