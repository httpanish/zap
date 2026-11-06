// Package history stores completed runs in an append-only JSONL file.
package history

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Path returns the location of the history file on this platform.
func Path() (string, error) {
	return resolvePath(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func resolvePath(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	fromHome := func(parts ...string) (string, error) {
		dir, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(append([]string{dir}, parts...)...), nil
	}
	switch goos {
	case "windows":
		dir := getenv("LocalAppData")
		if dir == "" {
			return "", errors.New("%LocalAppData% is not set")
		}
		return filepath.Join(dir, "zap", "history.jsonl"), nil
	case "darwin":
		return fromHome("Library", "Application Support", "zap", "history.jsonl")
	default:
		// The XDG specification says to ignore relative paths.
		if dir := getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
			return filepath.Join(dir, "zap", "history.jsonl"), nil
		}
		return fromHome(".local", "share", "zap", "history.jsonl")
	}
}
