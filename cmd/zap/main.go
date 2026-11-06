// Command zap is a visual speedtest CLI.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"golang.org/x/term"

	"github.com/httpanish/zap/internal/cloudflare"
	"github.com/httpanish/zap/internal/engine"
	"github.com/httpanish/zap/internal/history"
)

// Exit codes.
const (
	exitOK        = 0
	exitFailure   = 1
	exitUsage     = 2
	exitInterrupt = 130
	exitTerminate = 143
)

// Set at build time.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// app holds everything a command touches outside the process, so tests can
// replace it.
type app struct {
	stdout, stderr io.Writer
	interactive    bool // stdin and stdout are terminals, and TERM isn't dumb.
	stderrIsTTY    bool
	terminalWidth  func() (int, bool)
	location       *time.Location
	historyPath    func() (string, error)
	newClient      func() *cloudflare.Client
	tuneEngine     func(*engine.Config)
}

func main() {
	a := &app{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		interactive: isTerminal(os.Stdin) && isTerminal(os.Stdout) && os.Getenv("TERM") != "dumb",
		stderrIsTTY: isTerminal(os.Stderr),
		terminalWidth: func() (int, bool) {
			w, _, err := term.GetSize(int(os.Stdout.Fd()))
			return w, err == nil && w > 0
		},
		location:    time.Local,
		historyPath: history.Path,
		newClient:   func() *cloudflare.Client { return cloudflare.New(cloudflare.Config{Version: versionString()}) },
		tuneEngine:  func(*engine.Config) {},
	}
	os.Exit(a.run(os.Args[1:]))
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func (a *app) run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "history":
			return a.runHistory(args[1:])
		case "version":
			return a.runVersion(args[1:])
		}
	}
	return a.runTest(args)
}

func (a *app) warnf(format string, args ...any) {
	fmt.Fprintf(a.stderr, "zap: "+format+"\n", args...)
}

// parse parses flags. It returns an exit code and false if the command should
// stop: help exits with success, and invalid flags with a usage error.
func (a *app) parse(fs *flag.FlagSet, args []string) (int, bool) {
	fs.SetOutput(a.stderr)
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return exitOK, false
	case err != nil:
		return exitUsage, false
	}
	if fs.NArg() > 0 {
		a.warnf("unexpected argument %q", fs.Arg(0))
		return exitUsage, false
	}
	return exitOK, true
}

func (a *app) runVersion(args []string) int {
	if code, ok := a.parse(flag.NewFlagSet("zap version", flag.ContinueOnError), args); !ok {
		return code
	}
	line := "zap " + versionString()
	if commit != "" {
		line += " (" + commit
		if date != "" {
			line += ", " + date
		}
		line += ")"
	}
	fmt.Fprintln(a.stdout, line)
	return exitOK
}

// versionString prefers the version set at build time, and falls back to the
// module version that go install records.
func versionString() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
