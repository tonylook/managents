package detect

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
)

type fakeSource struct {
	name     string
	sessions []agent.Session
	err      error
}

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Sessions(context.Context, time.Time) ([]agent.Session, error) {
	return f.sessions, f.err
}

// slowSource answers after delay, unless its context is cut first — the way
// the database-backed sources honor their context.
type slowSource struct {
	name   string
	delay  time.Duration
	answer []agent.Session
}

func (s slowSource) Name() string { return s.name }

func (s slowSource) Sessions(ctx context.Context, _ time.Time) ([]agent.Session, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.delay):
		return s.answer, nil
	}
}

func TestAllMergesAndAttributesErrors(t *testing.T) {
	errLocked := errors.New("database is locked")
	healthy := fakeSource{name: "healthy", sessions: []agent.Session{{ID: "healthy:1"}, {ID: "healthy:2"}}}
	broken := fakeSource{name: "broken", sessions: []agent.Session{{ID: "broken:3"}}, err: errLocked}
	tests := []struct {
		name       string
		sources    []Source
		wantIDs    []string
		wantSource string // the source blamed for the error; empty for no error
	}{
		{"all healthy", []Source{healthy, fakeSource{name: "empty"}}, []string{"healthy:1", "healthy:2"}, ""},
		{"one failing", []Source{broken, healthy}, []string{"broken:3", "healthy:1", "healthy:2"}, "broken"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions, err := All(context.Background(), time.Now(), tt.sources)

			var ids []string
			for _, s := range sessions {
				ids = append(ids, s.ID)
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Errorf("sessions = %v, want %v: a failing source must not hide the others", ids, tt.wantIDs)
			}

			var sourceErr *SourceError
			switch {
			case tt.wantSource == "" && err != nil:
				t.Errorf("err = %v, want none", err)
			case tt.wantSource == "":
			case !errors.As(err, &sourceErr) || sourceErr.Source != tt.wantSource || !errors.Is(err, errLocked):
				t.Errorf("err = %v, want the %s source's error", err, tt.wantSource)
			case err.Error() != "broken: database is locked":
				t.Errorf("message = %q, want it prefixed with the source name", err.Error())
			}
		})
	}
}

// A source that runs past its deadline is cut: its error is blamed on it, and
// the other sources still contribute. Two slow sources prove they answer
// concurrently: the whole detection costs one deadline, not one per source,
// so a folder full of hostile repositories cannot push the frame past the
// display's KeepAlive.
func TestAllCutsASlowSourceAtItsDeadline(t *testing.T) {
	quick := fakeSource{name: "quick", sessions: []agent.Session{{ID: "quick:1"}}}
	slow := slowSource{name: "slow", delay: 10 * perSourceDeadline, answer: []agent.Session{{ID: "slow:1"}}}
	sluggish := slowSource{name: "sluggish", delay: 10 * perSourceDeadline, answer: []agent.Session{{ID: "sluggish:1"}}}

	started := time.Now()
	sessions, err := All(context.Background(), time.Now(), []Source{slow, sluggish, quick})
	took := time.Since(started)

	if took >= 2*perSourceDeadline {
		t.Errorf("detection took %v: the sources answer concurrently, so the worst case is one deadline", took)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline exceeded", err)
	}
	var sourceErr *SourceError
	if !errors.As(err, &sourceErr) || sourceErr.Source != "slow" {
		t.Errorf("err = %v, want the slow source blamed by name", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "quick:1" {
		t.Errorf("sessions = %+v, want only the quick source's: a cut source must not hide the others", sessions)
	}
}
