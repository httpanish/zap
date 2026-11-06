package main

import (
	"flag"
	"fmt"

	"github.com/httpanish/zap/internal/engine"
	"github.com/httpanish/zap/internal/history"
	"github.com/httpanish/zap/internal/output"
)

// fallbackWidth is the trend width when stdout isn't a terminal.
const fallbackWidth = 80

func (a *app) runHistory(args []string) int {
	fs := flag.NewFlagSet("zap history", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "number of most recent valid runs to show")
	asJSON := fs.Bool("json", false, "print the runs as a JSON array")
	if code, ok := a.parse(fs, args); !ok {
		return code
	}
	if *limit < 1 {
		a.warnf("--limit must be at least 1")
		return exitUsage
	}

	path, err := a.historyPath()
	if err != nil {
		a.warnf("could not find the history file: %v", err)
		return exitFailure
	}
	runs, skipped, err := history.Read(path, *limit)
	if err != nil {
		a.warnf("could not read history: %v", err)
		return exitFailure
	}
	if skipped > 0 {
		a.warnf("skipped %d unreadable history %s", skipped, plural(skipped, "line", "lines"))
	}

	if *asJSON {
		if runs == nil {
			runs = []engine.Result{} // Print [], never null.
		}
		if err := output.JSON(a.stdout, runs); err != nil {
			a.warnf("%v", err)
			return exitFailure
		}
		return exitOK
	}
	if len(runs) == 0 {
		fmt.Fprintln(a.stderr, "No history yet")
		return exitOK
	}
	width, ok := a.terminalWidth()
	if !ok {
		width = fallbackWidth
	}
	if err := output.HistoryTable(a.stdout, runs, width, a.location); err != nil {
		a.warnf("%v", err)
		return exitFailure
	}
	return exitOK
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
