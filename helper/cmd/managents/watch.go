package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
)

// watchInterval is how often status --watch repaints the table.
const watchInterval = 2 * time.Second

// clearScreen homes the cursor over a cleared screen, so each frame of
// status --watch replaces the last one instead of scrolling the terminal.
const clearScreen = "\x1b[H\x1b[2J"

// watchStatus keeps the status table on screen, repainting it every
// watchInterval, until ctx is done — Ctrl-C, or the signal that stops the
// program. Output that is not a terminal (a pipe, a file) cannot be
// repainted, so it gets exactly one report, as without --watch.
func watchStatus(ctx context.Context, inv *invocation, sources []detect.Source, home string) error {
	if f, ok := inv.stdout.(*os.File); !ok || !isatty.IsTerminal(f.Fd()) {
		return printStatusOnce(ctx, inv, sources, false, home)
	}
	w := &statusWatcher{
		out:      inv.stdout,
		errOut:   inv.stderr,
		sources:  sources,
		home:     home,
		interval: watchInterval,
		clock:    time.Now,
	}
	return w.run(ctx)
}

// statusWatcher repaints the status table. Its fields are the tests' seams;
// watchStatus fills them with the real ones.
type statusWatcher struct {
	out, errOut io.Writer
	sources     []detect.Source
	home        string
	interval    time.Duration
	clock       func() time.Time

	lastErrorMessage string
}

// run repaints at once, then at every tick, until ctx is done.
func (w *statusWatcher) run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	if err := w.repaint(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.repaint(ctx); err != nil {
				return err
			}
		}
	}
}

// repaint runs one detection pass and redraws the screen. A source that fails
// is not fatal: the next pass retries, and a new error is reported on stderr
// once, the way the runner logs its detection errors once per distinct error.
// A failure caused by ctx itself is not reported: the watch is ending, and
// its exit must stay clean.
func (w *statusWatcher) repaint(ctx context.Context) error {
	now := w.clock()
	sessions, err := detect.All(ctx, now, w.sources)
	message := ""
	if err != nil {
		message = err.Error()
	}
	if message != "" && message != w.lastErrorMessage && ctx.Err() == nil {
		fmt.Fprintf(w.errOut, "managents: session detection failed: %v\n", err)
	}
	w.lastErrorMessage = message
	cards := agent.Arrange(sessions)
	fmt.Fprint(w.out, clearScreen)
	return printStatus(w.out, cards, now, w.home)
}
