package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// maxLogSize is the size past which run --log-file starts a new log. Under
// the service, a program that keeps crashing is restarted every 10 s and
// would otherwise grow the log without bound.
const maxLogSize = 1 << 20

// startedMessage is logged first by every run, so the log can be read from
// the latest start.
const startedMessage = "managents started"

// newLogger logs to w at the given level.
func newLogger(w io.Writer, level string) (*slog.Logger, error) {
	l, err := parseLogLevel(level)
	if err != nil {
		return nil, err
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l})), nil
}

func parseLogLevel(level string) (slog.Level, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return 0, fmt.Errorf("invalid log level %q (use debug, info, warn, error)", level)
	}
	return l, nil
}

// openLogFile opens path for appending, creating it readable by the user
// only. A file larger than limit is first renamed to path.1, replacing the
// previous one. Under launchd, which opened path as stderr before the program
// started, a panic in a run that rotated the log therefore lands in path.1.
func openLogFile(path string, limit int64) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > limit {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}
