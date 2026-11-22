package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// watchSource is a detect.Source whose sessions (or failure) the test changes
// between ticks.
type watchSource struct {
	mu             sync.Mutex
	sessions       []agent.Session
	err            error
	failOnCanceled bool // report the context itself once it is done
}

func (s *watchSource) Name() string { return "watch" }

func (s *watchSource) Sessions(ctx context.Context, _ time.Time) ([]agent.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.sessions, s.err
	}
	if s.failOnCanceled && ctx.Err() != nil {
		return s.sessions, ctx.Err()
	}
	return s.sessions, nil
}

func (s *watchSource) set(sessions []agent.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = sessions
}

// recordingWriter is a concurrency-safe buffer: the watcher paints from its
// loop while the test reads.
type recordingWriter struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(p)
}

func (w *recordingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.String()
}

func TestWatchRepaintsChangedSessionsUntilCancelled(t *testing.T) {
	source := &watchSource{sessions: []agent.Session{
		{ID: "claude:a", Kind: agent.KindClaude, Dir: "/w/one", Status: agent.StatusWorking},
	}}
	var out, errOut recordingWriter
	w := &statusWatcher{
		out: &out, errOut: &errOut, sources: []detect.Source{source}, home: "",
		interval: 2 * time.Millisecond, clock: time.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.run(ctx) }()

	waitFor(t, func() bool { return strings.Count(out.String(), "NAME") >= 1 }, "the first frame")

	source.set([]agent.Session{
		{ID: "claude:b", Kind: agent.KindClaude, Dir: "/w/two", Status: agent.StatusWaiting},
	})
	waitFor(t, func() bool {
		frame := out.String()
		return strings.Count(frame, "NAME") >= 2 && strings.Contains(frame, "two")
	}, "a second frame with the changed session")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop when the context was cancelled")
	}
}

// TestWatchReportsAFailedSourceOnce replays the runner's once-per-distinct-
// error rule: a source that fails every pass is reported once, and the table
// of the sources that still work keeps being painted.
func TestWatchReportsAFailedSourceOnce(t *testing.T) {
	source := &watchSource{err: errors.New("read failed")}
	var out, errOut recordingWriter
	w := &statusWatcher{
		out: &out, errOut: &errOut,
		sources:  []detect.Source{source},
		home:     "",
		interval: time.Millisecond,
		clock:    time.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.run(ctx) }()

	waitFor(t, func() bool { return strings.Count(out.String(), clearScreen) >= 2 }, "two frames")
	if n := strings.Count(errOut.String(), "session detection failed"); n != 1 {
		t.Errorf("stderr = %q, want the failure reported once, not %d times", errOut.String(), n)
	}
	cancel()
	<-done
}

// A Ctrl-C that lands while a pass is running cancels the sources' context:
// the watch must not report that as a detection failure on its way out.
func TestWatchDoesNotReportTheShutdownAsAFailure(t *testing.T) {
	source := &watchSource{failOnCanceled: true}
	var out, errOut recordingWriter
	w := &statusWatcher{
		out: &out, errOut: &errOut, sources: []detect.Source{source}, home: "",
		interval: time.Hour, clock: time.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.repaint(ctx); err != nil {
		t.Fatal(err)
	}
	if errOut.String() != "" {
		t.Errorf("stderr = %q, want nothing: the cancelled context is not a detection failure", errOut.String())
	}
}

// The failure message carries file names from the user's config directories:
// a hostile name must not reach the terminal with its control bytes.
func TestWatchSanitizesTheFailureMessage(t *testing.T) {
	source := &watchSource{err: &fs.PathError{
		Op: "open", Path: "\x1b]0;owned\x07.json", Err: errors.New("no such file or directory")}}
	var out, errOut recordingWriter
	w := &statusWatcher{
		out: &out, errOut: &errOut, sources: []detect.Source{source}, home: "",
		interval: time.Hour, clock: time.Now,
	}
	if err := w.repaint(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(errOut.String(), "\x1b\x07") {
		t.Errorf("control bytes reached the terminal: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "owned") {
		t.Errorf("the message was dropped rather than sanitized: %q", errOut.String())
	}
}

func TestWatchOnANonTerminalPrintsOneReport(t *testing.T) {
	source := &watchSource{sessions: []agent.Session{
		{ID: "claude:a", Kind: agent.KindClaude, Dir: "/w/one", Status: agent.StatusWorking},
	}}
	inv := &invocation{name: "status", stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := watchStatus(context.Background(), inv, []detect.Source{source}, ""); err != nil {
		t.Fatal(err)
	}
	out := inv.stdout.(*bytes.Buffer).String()
	if strings.Count(out, "NAME") != 1 {
		t.Errorf("output = %q, want exactly one report", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("output carries cursor controls: %q", out)
	}
}

// waitFor polls until the condition holds, failing the test after a second.
func waitFor(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("never saw " + what)
}
