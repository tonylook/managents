// Package detect finds the agent sessions open on this computer.
//
// Each agent product has its own Source (see the claude, opencode and codex
// subpackages). Sources only read session metadata — status, working
// directory, timestamps, token counts — never conversation content.
package detect

import (
	"context"
	"errors"
	"sync"
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

// All queries every source and merges the results. The sources answer
// concurrently, each under its own deadline, and the merge keeps their order:
// a source that fails, or that runs past its deadline, is blamed by name
// (SourceError) and costs the others nothing.
func All(ctx context.Context, now time.Time, sources []Source) ([]agent.Session, error) {
	type answered struct {
		sessions []agent.Session
		err      error
	}
	answers := make([]answered, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sourceCtx, cancel := context.WithTimeout(ctx, perSourceDeadline)
			defer cancel()
			answers[i].sessions, answers[i].err = source.Sessions(sourceCtx, now)
		}()
	}
	wg.Wait()
	var sessions []agent.Session
	var errs []error
	for i, answer := range answers {
		if answer.err != nil {
			errs = append(errs, &SourceError{Source: sources[i].Name(), Err: answer.err})
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
