package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/httpanish/zap/internal/engine"
)

// Append adds a run as one line, with a single O_APPEND write, so whole lines
// don't interleave in normal use when two runs overlap.
func Append(path string, r *engine.Result) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Read returns the most recent valid runs, newest first, keeping file order for
// ties. Skipped counts malformed lines and lines with an unknown schema version.
// A missing file is not an error, and gives no runs.
func Read(path string, limit int) (runs []engine.Result, skipped int, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if r, ok := parseLine(line); ok {
				runs = append(runs, r)
			} else {
				skipped++
			}
		}
		if err != nil {
			break
		}
	}

	slices.SortStableFunc(runs, func(a, b engine.Result) int {
		return b.Timestamp.Compare(a.Timestamp)
	})
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, skipped, nil
}

func parseLine(line []byte) (engine.Result, bool) {
	var r engine.Result
	if err := json.Unmarshal(line, &r); err != nil {
		return r, false
	}
	if r.SchemaVersion != engine.SchemaVersion || r.Timestamp.IsZero() {
		return r, false
	}
	// Keep the promise that samples are an array, never null.
	if r.Download.SamplesMbps == nil {
		r.Download.SamplesMbps = []engine.Dec1{}
	}
	if r.Upload.SamplesMbps == nil {
		r.Upload.SamplesMbps = []engine.Dec1{}
	}
	return r, true
}
