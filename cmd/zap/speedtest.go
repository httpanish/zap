package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/httpanish/zap/internal/card"
	"github.com/httpanish/zap/internal/engine"
	"github.com/httpanish/zap/internal/history"
	"github.com/httpanish/zap/internal/output"
	"github.com/httpanish/zap/internal/tui"
)

const usage = `Usage:
  zap [flags]         Run a speed test
  zap history         Show past results
  zap version         Print version information

Flags:
  --duration <d>      Length of each transfer phase, 3s to 60s (default 10s)
  --streams <n>       Parallel connections per transfer phase, 1 to 32 (default 4)
  --json              Print the result as JSON, with no TUI
  --card              Export a result card to the current directory
  --card-dir <dir>    Directory for the result card; also turns on --card
  --no-save           Don't append this run to history
`

const (
	minDuration = 3 * time.Second
	maxDuration = 60 * time.Second
	minStreams  = 1
	maxStreams  = 32
)

type testOptions struct {
	duration time.Duration
	streams  int
	asJSON   bool
	cardOn   bool
	cardDir  string
	noSave   bool
}

// parseTestFlags returns the options, or an exit code and false if the command
// should stop.
func (a *app) parseTestFlags(args []string) (testOptions, int, bool) {
	var o testOptions
	fs := flag.NewFlagSet("zap", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(a.stderr, usage) }
	fs.DurationVar(&o.duration, "duration", 10*time.Second, "")
	fs.IntVar(&o.streams, "streams", 4, "")
	fs.BoolVar(&o.asJSON, "json", false, "")
	fs.BoolVar(&o.cardOn, "card", false, "")
	fs.StringVar(&o.cardDir, "card-dir", ".", "")
	fs.BoolVar(&o.noSave, "no-save", false, "")
	if code, ok := a.parse(fs, args); !ok {
		return o, code, false
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "card-dir" {
			o.cardOn = true
		}
	})
	if o.duration < minDuration || o.duration > maxDuration {
		a.warnf("--duration must be between %s and %s", minDuration, maxDuration)
		return o, exitUsage, false
	}
	if o.streams < minStreams || o.streams > maxStreams {
		a.warnf("--streams must be between %d and %d", minStreams, maxStreams)
		return o, exitUsage, false
	}
	if o.cardOn {
		if err := card.CheckDir(o.cardDir); err != nil {
			a.warnf("can't write a card to %s: %v", o.cardDir, err)
			return o, exitUsage, false
		}
	}
	return o, exitOK, true
}

func (a *app) runTest(args []string) int {
	opts, code, ok := a.parseTestFlags(args)
	if !ok {
		return code
	}
	client := a.newClient()
	cfg := engine.Config{Duration: opts.duration, Streams: opts.streams}
	a.tuneEngine(&cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := engine.Start(ctx, cfg, client)

	useTUI := a.interactive && !opts.asJSON
	warnings := []string{}
	if w := client.ProxyWarning(); w != "" {
		warnings = append(warnings, w)
	}
	flushWarnings := func() {
		for _, w := range warnings {
			fmt.Fprintln(a.stderr, w)
		}
		warnings = nil
	}

	var program *tea.Program
	if useTUI {
		// Anything for stderr waits until the alternate screen closes.
		program = tea.NewProgram(tui.New(events, cancel), tea.WithAltScreen(), tea.WithoutSignalHandler())
	} else {
		flushWarnings()
	}

	var signalCode atomic.Int32
	stopSignals := a.watchSignals(cancel, program, &signalCode)
	defer stopSignals()

	var finished engine.Finished
	var cancelled bool
	if useTUI {
		final, err := program.Run()
		if err != nil {
			cancel()
			a.warnf("terminal error: %v", err)
			return exitFailure
		}
		m := final.(tui.Model)
		finished = engine.Finished{Result: m.Result(), Err: m.Err()}
		cancelled = m.Cancelled()
	} else {
		finished = a.consume(events)
	}

	if c := signalCode.Load(); c != 0 {
		return int(c)
	}
	if cancelled || errors.Is(finished.Err, context.Canceled) {
		return exitInterrupt
	}
	if finished.Err != nil {
		a.warnf("%v", finished.Err)
		flushWarnings()
		return exitFailure
	}
	return a.deliver(finished.Result, opts, flushWarnings)
}

// watchSignals handles SIGINT and SIGTERM in every mode. The first cancels the
// run, and a second exits immediately, in case cleanup hangs. It returns a
// function that stops watching.
func (a *app) watchSignals(cancel func(), program *tea.Program, code *atomic.Int32) func() {
	sigs := make(chan os.Signal, 2)
	done := make(chan struct{})
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigs:
			exit := int32(exitInterrupt)
			if sig == syscall.SIGTERM {
				exit = exitTerminate
			}
			code.Store(exit)
			cancel()
			if program != nil {
				program.Quit()
			}
			select {
			case <-sigs:
				os.Exit(int(exit))
			case <-done:
			}
		case <-done:
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

// consume drains events without a TUI. If stderr is a terminal, it prints one
// plain line per phase there.
func (a *app) consume(events <-chan engine.Event) engine.Finished {
	var finished engine.Finished
	for ev := range events {
		switch e := ev.(type) {
		case engine.PhaseStarted:
			if a.stderrIsTTY {
				fmt.Fprintln(a.stderr, phaseLine(e.Phase))
			}
		case engine.Finished:
			finished = e
		}
	}
	return finished
}

func phaseLine(p engine.Phase) string {
	switch p {
	case engine.PhaseConnect:
		return "Connecting..."
	case engine.PhaseIdle:
		return "Measuring latency..."
	}
	return fmt.Sprintf("Measuring %s...", p)
}

// deliver prints the result, saves history, then writes the card, in that order,
// so a card failure can't lose anything.
func (a *app) deliver(r *engine.Result, opts testOptions, flushWarnings func()) int {
	code := exitOK
	var err error
	if opts.asJSON {
		err = output.JSON(a.stdout, r)
	} else {
		err = output.Summary(a.stdout, r)
	}
	if err != nil {
		a.warnf("could not print the result: %v", err)
		code = exitFailure
	}
	flushWarnings()

	if !opts.noSave {
		if err := a.save(r); err != nil {
			a.warnf("could not save this run to history: %v", err)
		}
	}
	if opts.cardOn {
		path, err := card.Write(opts.cardDir, r, a.location)
		if err != nil {
			a.warnf("could not write the card: %v", err)
			return exitFailure
		}
		fmt.Fprintf(a.stderr, "Card written to %s\n", path)
	}
	return code
}

func (a *app) save(r *engine.Result) error {
	path, err := a.historyPath()
	if err != nil {
		return err
	}
	return history.Append(path, r)
}
