// Package detect finds the agent sessions open on this computer.
//
// Each agent product has its own Source (see the claude, opencode and codex
// subpackages). Sources only read session metadata — status, working
// directory, timestamps, token counts — never conversation content.
package detect

import (
	"context"
	"errors"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
)

// Source detects the open sessions of one agent product.
type Source interface {
	Name() string
	Sessions(ctx context.Context, now time.Time) ([]agent.Session, error)
}

// perSourceDeadline bounds how long one source may take to answer. A hostile
// repository can cost a detector most of a poll — an unindexed crush.db, say
// 95 ms per folder — and the sources answering one after another would let a
// dozen such folders push the frame past the display's 2 s KeepAlive, until
// the display flaps to Reconnecting. 500 ms covers two back-to-back sqlite
// busy waits (the crush reader waits 250 ms per query) with a little margin,
// and the sources answer concurrently, so the worst case is one deadline,
// not twelve.
const perSourceDeadline = 500 * time.Millisecond

// abandonAfter is how much longer than its own deadline a source gets before
// All gives up on it for good. A source that honors its context answers by
// its deadline; one still silent afterwards is stuck in a call its context
// cannot reach — an open blocked on a named pipe, say — and is abandoned:
// blamed by name like a source that ran past its deadline, its answer
// dropped if it ever arrives. Without this, one hostile file would hold
// every card hostage until the helper is restarted, for the poll loop waits
// for All.
const abandonAfter = 250 * time.Millisecond

// All queries every source and merges the results. The sources answer
// concurrently, each under its own deadline, and the merge keeps their order:
// a source that fails, runs past its deadline, or is abandoned as stuck is
// blamed by name (SourceError) and costs the others nothing.
func All(ctx context.Context, now time.Time, sources []Source) ([]agent.Session, error) {
	type answered struct {
		sessions []agent.Session
		err      error
	}
	type reply struct {
		i   int
		ans answered
	}
	// Buffered, so the goroutine of an abandoned source can still deliver
	// (to nobody) and end instead of blocking on the send forever.
	replies := make(chan reply, len(sources))
	for i, source := range sources {
		go func() {
			sourceCtx, cancel := context.WithTimeout(ctx, perSourceDeadline)
			defer cancel()
			sessions, err := source.Sessions(sourceCtx, now)
			replies <- reply{i: i, ans: answered{sessions, err}}
		}()
	}
	answers := make([]answered, len(sources))
	arrived := make([]bool, len(sources))
	giveUp := time.After(perSourceDeadline + abandonAfter)
	for pending := len(sources); pending > 0; {
		select {
		case r := <-replies:
			answers[r.i] = r.ans
			arrived[r.i] = true
			pending--
		case <-giveUp:
			pending = 0 // abandon whoever is left
		}
	}
	var sessions []agent.Session
	var errs []error
	for i, answer := range answers {
		switch {
		case arrived[i] && answer.err != nil:
			errs = append(errs, &SourceError{Source: sources[i].Name(), Err: answer.err})
		case !arrived[i]:
			errs = append(errs, &SourceError{Source: sources[i].Name(), Err: context.DeadlineExceeded})
		}
		sessions = append(sessions, answer.sessions...)
	}
	return sessions, errors.Join(errs...)
}

// SourceError attributes a detection failure to its source.
type SourceError struct {
	Source string
	Err    error
}

func (e *SourceError) Error() string { return e.Source + ": " + e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }
