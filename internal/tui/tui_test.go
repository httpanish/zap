package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/httpanish/zap/internal/engine"
)

func feed(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func size(w, h int) tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }

func event(e engine.Event) tea.Msg { return eventMsg{e} }

func float(v float64) *float64 { return &v }

// midDownload is a model partway through the download phase, as in the spec's sketch.
func midDownload(t *testing.T) Model {
	t.Helper()
	series := make([]float64, 60)
	for i := range series {
		series[i] = 100 + float64(i)*5
	}
	return feed(t, New(nil, nil),
		event(engine.PhaseStarted{Phase: engine.PhaseConnect}),
		event(engine.Connected{Server: engine.Server{Provider: "cloudflare", Colo: "LHR"}}),
		event(engine.PhaseStarted{Phase: engine.PhaseIdle}),
		event(engine.IdleDone{IdleMs: 12, JitterMs: 2}),
		event(engine.PhaseStarted{Phase: engine.PhaseDownload}),
		event(engine.Progress{Phase: engine.PhaseDownload, Fraction: 0.6, Mbps: 412, Series: series, LoadedMs: float(50), TotalBytes: 206_000_000}),
	)
}

func wantContains(t *testing.T, view string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func wantAbsent(t *testing.T, view string, absents ...string) {
	t.Helper()
	for _, absent := range absents {
		if strings.Contains(view, absent) {
			t.Errorf("view shouldn't contain %q:\n%s", absent, view)
		}
	}
}

func wantFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Errorf("view is %d lines, more than %d:\n%s", len(lines), height, view)
	}
	for _, line := range lines {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line is %d columns, more than %d: %q", w, width, line)
		}
	}
}

func TestViewAt80Columns(t *testing.T) {
	v := feed(t, midDownload(t), size(80, 24)).View()
	wantContains(t, v, "zap", "Cloudflare · LHR", "PING", "12 ms", "jitter 2 ms",
		"DOWNLOAD", "412 Mbps", "loaded 50 ms (+38)", "UPLOAD", "waiting...", "q quit", "data used 206.0 MB", "█", "░")
	wantFits(t, v, 80, 24)
}

func TestViewAt40ColumnsDropsSecondaryText(t *testing.T) {
	v := feed(t, midDownload(t), size(40, 24)).View()
	wantContains(t, v, "zap", "PING", "12 ms", "DOWNLOAD", "412 Mbps", "UPLOAD", "q quit")
	wantAbsent(t, v, "jitter", "loaded", "data used", "Cloudflare")
	wantFits(t, v, 40, 24)
}

func TestViewFitsAtTheMinimumSizeWithBothPhasesActive(t *testing.T) {
	series := make([]float64, 100)
	m := feed(t, midDownload(t),
		size(minWidth, minHeight),
		event(engine.TransferDone{Phase: engine.PhaseDownload, Mbps: 1412.3, Series: series}),
		event(engine.PhaseStarted{Phase: engine.PhaseUpload}),
		event(engine.Progress{Phase: engine.PhaseUpload, Fraction: 0.5, Mbps: 900, Series: series}),
	)
	v := m.View()
	wantContains(t, v, "DOWNLOAD", "UPLOAD")
	wantFits(t, v, minWidth, minHeight)
}

func TestCompactBelow40Columns(t *testing.T) {
	v := feed(t, midDownload(t), size(39, 24)).View()
	if v != "download 60%  412 Mbps" {
		t.Errorf("view = %q", v)
	}
}

func TestCompactBelow11Rows(t *testing.T) {
	v := feed(t, midDownload(t), size(80, 10)).View()
	if v != "download 60%  412 Mbps" {
		t.Errorf("view = %q", v)
	}
}

func TestCompactIsTruncatedToTheWidth(t *testing.T) {
	v := feed(t, midDownload(t), size(8, 3)).View()
	if v != "download" {
		t.Errorf("view = %q", v)
	}
}

func TestCompactInEachPhase(t *testing.T) {
	tests := []struct {
		name string
		msgs []tea.Msg
		want string
	}{
		{"connect", []tea.Msg{event(engine.PhaseStarted{Phase: engine.PhaseConnect})}, "connecting..."},
		{"idle", []tea.Msg{
			event(engine.PhaseStarted{Phase: engine.PhaseIdle}),
			event(engine.Progress{Phase: engine.PhaseIdle, Fraction: 0.4}),
		}, "latency 40%"},
		{"upload", []tea.Msg{
			event(engine.PhaseStarted{Phase: engine.PhaseUpload}),
			event(engine.Progress{Phase: engine.PhaseUpload, Fraction: 0.5, Mbps: 48}),
		}, "upload 50%  48 Mbps"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := feed(t, New(nil, nil), size(30, 5))
			if v := feed(t, m, tt.msgs...).View(); v != tt.want {
				t.Errorf("view = %q, want %q", v, tt.want)
			}
		})
	}
}

func TestResizeWorksInBothDirections(t *testing.T) {
	m := midDownload(t)
	m = feed(t, m, size(80, 24))
	wantContains(t, m.View(), "DOWNLOAD")
	m = feed(t, m, size(30, 24))
	if v := m.View(); strings.Contains(v, "\n") || !strings.Contains(v, "download") {
		t.Errorf("view after shrinking = %q", v)
	}
	m = feed(t, m, size(80, 24))
	wantContains(t, m.View(), "DOWNLOAD", "Cloudflare · LHR")
}

func TestSparklineFitsAndResamplesAtEachWidth(t *testing.T) {
	series := make([]float64, 600)
	for i := range series {
		series[i] = float64(i)
	}
	for _, width := range []int{40, 80, 200} {
		m := feed(t, New(nil, nil),
			size(width, 24),
			event(engine.PhaseStarted{Phase: engine.PhaseDownload}),
			event(engine.TransferDone{Phase: engine.PhaseDownload, Mbps: 100, Series: series}),
		)
		wantFits(t, m.View(), width, 24)
	}
}

func TestPhaseEndShowsHeadlineAndKeepsSparkline(t *testing.T) {
	m := feed(t, midDownload(t), event(engine.TransferDone{
		Phase: engine.PhaseDownload, Mbps: 398.4, Series: []float64{1, 2, 3}, LoadedMs: float(50), TotalBytes: 400_000_000,
	}))
	v := m.View()
	wantContains(t, v, "398 Mbps", "▃", "█", "data used 400.0 MB")
	wantAbsent(t, v, "412 Mbps", "░")
}

func TestLoadedDisplay(t *testing.T) {
	tests := []struct {
		name string
		done engine.TransferDone
		want string
	}{
		{"no usable samples", engine.TransferDone{Phase: engine.PhaseDownload}, "loaded n/a"},
		{"lower bound", engine.TransferDone{Phase: engine.PhaseDownload, LoadedMs: float(50), LoadedLowerBound: true}, "loaded >=50 ms (+38)"},
		{"below idle shows +0", engine.TransferDone{Phase: engine.PhaseDownload, LoadedMs: float(8)}, "loaded 8 ms (+0)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantContains(t, feed(t, midDownload(t), event(tt.done)).View(), tt.want)
		})
	}
}

func TestLateProgressCantOverwriteAFinishedPhase(t *testing.T) {
	m := feed(t, midDownload(t),
		event(engine.TransferDone{Phase: engine.PhaseDownload, Mbps: 398}),
		event(engine.Progress{Phase: engine.PhaseDownload, Fraction: 0.9, Mbps: 1}),
	)
	wantContains(t, m.View(), "398 Mbps")
}

func TestQuitKeysCancel(t *testing.T) {
	keys := map[string]tea.KeyMsg{
		"q":      {Type: tea.KeyRunes, Runes: []rune("q")},
		"esc":    {Type: tea.KeyEsc},
		"ctrl+c": {Type: tea.KeyCtrlC},
	}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			cancelled := false
			next, cmd := New(nil, func() { cancelled = true }).Update(key)
			m := next.(Model)
			if !cancelled || !m.Cancelled() {
				t.Error("quitting didn't cancel the test")
			}
			if cmd == nil {
				t.Fatal("no command returned")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Error("quitting didn't quit the program")
			}
		})
	}
}

func TestOtherKeysDoNothing(t *testing.T) {
	cancelled := false
	next, _ := New(nil, func() { cancelled = true }).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cancelled || next.(Model).Cancelled() {
		t.Error("an unrelated key cancelled the test")
	}
}

func TestFinishedQuitsWithTheOutcome(t *testing.T) {
	result := &engine.Result{}
	next, cmd := New(nil, nil).Update(event(engine.Finished{Result: result}))
	if next.(Model).Result() != result {
		t.Error("result wasn't kept")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("finishing didn't quit")
	}

	failure := errors.New("boom")
	next, _ = New(nil, nil).Update(event(engine.Finished{Err: failure}))
	if next.(Model).Err() != failure {
		t.Error("error wasn't kept")
	}
}

func TestNextPumpsEventsUntilClosed(t *testing.T) {
	ch := make(chan engine.Event, 1)
	ch <- engine.PhaseStarted{Phase: engine.PhaseIdle}
	m := New(ch, nil)
	if _, ok := m.Init()().(eventMsg); !ok {
		t.Error("first command didn't deliver the event")
	}
	close(ch)
	if _, ok := m.next()().(closedMsg); !ok {
		t.Error("a closed channel wasn't reported")
	}
}
