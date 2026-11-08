// Package zcode detects ZCode sessions.
//
// Every running ZCode session is a process for its working directory. On
// Linux that process is the `zcode-cli` binary; the desktop app, on every
// platform, runs each conversation in an Electron helper whose process title
// it rewrites to "zcode-cli" (see isSession), so the folder is the only mark
// a session leaves beyond that title. Sessions the app runs for itself, in
// the copies of a project under ~/.zcode and in its own folders there, get no
// card, like Codex's `codex exec`.
//
// ZCode records a folder's conversations in a SQLite database (see
// SQLiteStore); the one it updated last is the open conversation. It admits a
// prompt to `session_input` the moment it is sent, but writes a turn's
// `turn_usage` row only when the turn ends, with its outcome — so a prompt
// without its turn is a turn in progress.
//
// ZCode records neither a pending approval nor the model's context window: a
// session blocked on an approval shows as working until it is answered, and
// its cards have no context bar.
package zcode

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

// Session is the part of a ZCode conversation this package needs.
type Session struct {
	ID string
}

// Turn is the newest turn ZCode recorded for a conversation. It writes the
// row when the turn ends.
type Turn struct {
	Status          string // "running", "completed", "error" or "cancelled"
	StartedAt       time.Time
	CompletedAt     time.Time // zero while the turn runs
	CancelledByUser bool
}

// Input is the newest prompt admitted to a conversation.
type Input struct {
	SentAt time.Time
}

// Store reads ZCode's persisted session data.
type Store interface {
	// LastSession is the open conversation of a folder: the session ZCode
	// updated last there.
	LastSession(ctx context.Context, dir string) (Session, bool, error)
	// LastTurn is the newest turn of a conversation.
	LastTurn(ctx context.Context, sessionID string) (Turn, bool, error)
	// LastInput is the newest prompt admitted to a conversation.
	LastInput(ctx context.Context, sessionID string) (Input, bool, error)
}

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running ZCode sessions.
type Source struct {
	Processes ProcessFinder
	Store     Store
	// dataDir is ZCode's own folder, ~/.zcode: the copies its automations
	// work in and the processes it runs for itself there get no card.
	dataDir string
}

// NewSource returns a Source for the ZCode installation of the user whose
// home directory is home.
func NewSource(home string, processes ProcessFinder) *Source {
	return &Source{
		Processes: processes,
		Store:     SQLiteStore{Path: DefaultDatabasePath(home)},
		dataDir:   filepath.Join(home, ".zcode"),
	}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindZCode) }

// Sessions implements detect.Source. A folder whose history cannot be read
// is still shown, as waiting, and the store errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	var procs []process.Info
	for _, name := range []string{"zcode-cli", "ZCode Helper"} {
		found, err := s.Processes.FindByName(ctx, name)
		if err != nil {
			return nil, err
		}
		procs = append(procs, found...)
	}
	// The oldest process in a folder owns its card, as for OpenCode: the
	// card keeps its ID when another conversation starts there.
	slices.SortFunc(procs, func(a, b process.Info) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.PID, b.PID))
	})
	var sessions []agent.Session
	var errs []error
	seen := make(map[string]bool)
	for _, p := range procs {
		if p.Dir == "" || seen[p.Dir] || !isSession(p) || within(p.Dir, s.dataDir) {
			continue // one look per folder; the app's plumbing is not a session
		}
		seen[p.Dir] = true
		session, err := s.toSession(ctx, p, now)
		sessions = append(sessions, session)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// isSession reports a process that runs a conversation. The binary is called
// zcode-cli; the desktop app runs its conversations in Electron helpers
// (executable name "ZCode Helper") whose process title it rewrites to
// "zcode-cli", while its other helpers keep the path of their binary there
// and its services announce themselves as "zcode-host-local-1" and friends.
func isSession(p process.Info) bool {
	if p.Name == "zcode-cli" {
		return true
	}
	return p.Name == "ZCode Helper" && len(p.Args) > 0 && filepath.Base(p.Args[0]) == "zcode-cli"
}

// toSession builds the card of a folder: the conversation ZCode updated last
// there, waiting since it was opened when its history cannot be read.
func (s *Source) toSession(ctx context.Context, p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindZCode) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindZCode,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	conversation, found, err := s.Store.LastSession(ctx, p.Dir)
	if err != nil || !found {
		return session, err // ZCode writes the session when it opens the conversation
	}
	input, hasInput, inputErr := s.Store.LastInput(ctx, conversation.ID)
	turn, hasTurn, turnErr := s.Store.LastTurn(ctx, conversation.ID)
	session.Status, session.Since = statusOf(hasInput, input, hasTurn, turn, p.StartedAt, now)
	return session, errors.Join(inputErr, turnErr)
}

// statusOf maps the newest prompt and turn of a conversation to a status and
// when it began. started is when the session's `zcode-cli` process started:
// a prompt or turn written before that belongs to a previous conversation in
// the folder, so it is neither working nor this run's error, and the session
// waits since it was opened.
func statusOf(hasInput bool, input Input, hasTurn bool, turn Turn, started, now time.Time) (agent.Status, time.Time) {
	// ZCode starts a turn a few milliseconds after admitting its prompt, so
	// the turn of an earlier prompt began before the newest one: the newest
	// prompt's turn is still running.
	if hasInput && !input.SentAt.Before(started) && (!hasTurn || turn.StartedAt.Before(input.SentAt)) {
		return agent.StatusWorking, input.SentAt
	}
	if hasTurn && turn.Status == "running" && !turn.StartedAt.Before(started) {
		return agent.StatusWorking, turn.StartedAt // a turn ZCode has not closed
	}
	since, live := started, false
	if hasTurn && !turn.CompletedAt.IsZero() {
		live = !turn.StartedAt.Before(started)
		since = turn.CompletedAt
		if !live {
			since = started // a turn of a previous run is not this card's age
		}
	}
	switch {
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	case live && turn.Status == "error" && !turn.CancelledByUser:
		return agent.StatusError, since
	default:
		return agent.StatusWaiting, since
	}
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return path != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
