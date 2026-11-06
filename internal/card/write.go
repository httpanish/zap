package card

import (
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/httpanish/zap/internal/engine"
)

// CheckDir verifies before the test that the directory exists, is a directory,
// and is writable, by creating and removing a temp file. It never creates the
// directory, so a typo can't produce a stray folder.
func CheckDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".zap-check-*")
	if err != nil {
		return err
	}
	return errors.Join(f.Close(), os.Remove(f.Name()))
}

// Filename returns the card's name, in the given zone. Including seconds means
// two runs won't collide.
func Filename(t time.Time, loc *time.Location) string {
	return "zap-" + t.In(loc).Format("2006-01-02-150405") + ".png"
}

// Write renders a card into dir and returns its path. It writes to a temp file
// and renames it, so a failure can't leave a partial card behind.
func Write(dir string, r *engine.Result, loc *time.Location) (path string, err error) {
	tmp, err := os.CreateTemp(dir, ".zap-card-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	if err := png.Encode(tmp, Render(NewLayout(r, loc))); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	// CreateTemp makes private files, but a card is meant to be shared.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	path = filepath.Join(dir, Filename(r.Timestamp, loc))
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
