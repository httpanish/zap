// Package tui renders engine events as a live Bubble Tea view.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/httpanish/zap/internal/engine"
)

// transfer is the display state of one transfer phase.
type transfer struct {
	started    bool
	done       bool
	fraction   float64
	mbps       float64
	series     []float64
	loaded     *float64
	lowerBound bool
}

// Model is the Bubble Tea model for a running test.
type Model struct {
	events <-chan engine.Event
	cancel func()

	width, height int
	phase         engine.Phase
	colo          string
	idleFraction  float64
	idleDone      bool
	idleMs        float64
	jitterMs      float64
	download      transfer
	upload        transfer
	totalBytes    int64
	frame         int // Bolt animation frame.

	finished  bool
	result    *engine.Result
	err       error
	cancelled bool
}

// New returns a model that consumes events and calls cancel if the user quits.
func New(events <-chan engine.Event, cancel func()) Model {
	return Model{events: events, cancel: cancel, width: 80, height: 24}
}

// Result returns the completed result, if there is one.
func (m Model) Result() *engine.Result { return m.result }

// Err returns the error that ended the test, if there is one.
func (m Model) Err() error { return m.err }

// Cancelled reports whether the user quit before the test finished.
func (m Model) Cancelled() bool { return m.cancelled }

type eventMsg struct{ event engine.Event }

type closedMsg struct{}

type tickMsg struct{}

// tickEvery matches the redraw rate, so each tick moves the bolt one frame.
const tickEvery = 100 * time.Millisecond

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) next() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return closedMsg{}
		}
		return eventMsg{ev}
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return tea.Batch(m.next(), tick()) }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
	case closedMsg:
		return m, tea.Quit
	case tickMsg:
		m.frame++
		return m, tick()
	case eventMsg:
		m = m.apply(msg.event)
		if m.finished {
			return m, tea.Quit
		}
		return m, m.next()
	}
	return m, nil
}

func (m Model) apply(event engine.Event) Model {
	switch e := event.(type) {
	case engine.PhaseStarted:
		m.phase = e.Phase
		if t := m.transferFor(e.Phase); t != nil {
			t.started = true
		}
	case engine.Connected:
		m.colo = e.Server.Colo
	case engine.IdleDone:
		m.idleDone, m.idleMs, m.jitterMs = true, e.IdleMs, e.JitterMs
	case engine.Progress:
		if e.Phase == engine.PhaseIdle {
			m.idleFraction = e.Fraction
		}
		if t := m.transferFor(e.Phase); t != nil && !t.done {
			t.fraction, t.mbps, t.series, t.loaded = e.Fraction, e.Mbps, e.Series, e.LoadedMs
			m.totalBytes = e.TotalBytes
		}
	case engine.TransferDone:
		if t := m.transferFor(e.Phase); t != nil {
			t.done, t.fraction, t.mbps, t.series = true, 1, e.Mbps, e.Series
			t.loaded, t.lowerBound = e.LoadedMs, e.LoadedLowerBound
			m.totalBytes = e.TotalBytes
		}
	case engine.Finished:
		m.finished, m.result, m.err = true, e.Result, e.Err
	}
	return m
}

// transferFor returns the state of a transfer phase. The model is copied on each
// update, and the returned pointer is into this copy.
func (m *Model) transferFor(p engine.Phase) *transfer {
	switch p {
	case engine.PhaseDownload:
		return &m.download
	case engine.PhaseUpload:
		return &m.upload
	}
	return nil
}
