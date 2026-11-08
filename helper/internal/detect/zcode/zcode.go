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
// ZCode records every conversation of a folder in a SQLite database (see
// SQLiteStore), so a folder with several conversations running gets a card
// each. It admits a prompt to `session_input` the moment it is sent, but
// writes a turn's `turn_usage` row only when the turn ends, with its
// outcome — so a prompt without its turn is a turn in progress.
//
// ZCode records no close: it never archives a conversation and deletes one
// only when the user does. So a conversation ZCode has not written to for
// quietFor is treated as closed or abandoned and gets no card, while the
// processes say the folder still runs one. A folder whose conversations
// cannot be read is never hidden: it is one waiting card.
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

// quietFor is how long after a conversation's last write its card stays.
// ZCode records no close, so a conversation it has not written to for this
// long counts as closed or abandoned, however open it still looks in the
// app. Working conversations are kept whatever their write age: a turn can
// run long after its prompt.
const quietFor = 5 * time.Minute

// Session is the part of a ZCode conversation this package needs.
type Session struct {
	ID        string
	CreatedAt time.Time // when the conversation was opened
	UpdatedAt time.Time // the last write ZCode made to it
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
	// Sessions is a folder's open conversations, the one ZCode updated last
	// first.
	Sessions(ctx context.Context, dir string) ([]Session, error)
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
	// The oldest process in a folder owns it, as for OpenCode: its
	// conversations are read once, and the waiting card, when they cannot be,
	// keeps that process's pid.
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
		cards, err := s.cards(ctx, p, now)
		sessions = append(sessions, cards...)
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

// cards returns a folder's cards: one per conversation the database records
// there, or one card for the folder when its conversations cannot be read.
func (s *Source) cards(ctx context.Context, p process.Info, now time.Time) ([]agent.Session, error) {
	conversations, err := s.Store.Sessions(ctx, p.Dir)
	if err != nil || len(conversations) == 0 {
		return []agent.Session{waitingCard(p)}, err
	}
	var (
		sessions []agent.Session
		errs     []error
	)
	for _, c := range conversations {
		session, ok, sessionErr := s.toSession(ctx, c, p.Dir, now)
		errs = append(errs, sessionErr)
		if ok {
			sessions = append(sessions, session)
		}
	}
	return sessions, errors.Join(errs...)
}

// waitingCard is the card of a folder whose conversations cannot be read,
// or that has none in the database yet: it has a process, so something is
// open in it.
func waitingCard(p process.Info) agent.Session {
	return agent.Session{
		ID:        string(agent.KindZCode) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindZCode,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
}

// toSession builds the card of one conversation. ok is false for one ZCode
// has not written to for quietFor: ZCode records no close, so a conversation
// this quiet is a closed or an abandoned one.
func (s *Source) toSession(ctx context.Context, c Session, dir string, now time.Time) (agent.Session, bool, error) {
	input, hasInput, inputErr := s.Store.LastInput(ctx, c.ID)
	turn, hasTurn, turnErr := s.Store.LastTurn(ctx, c.ID)
	err := errors.Join(inputErr, turnErr)
	status, since := statusOf(hasInput, input, hasTurn, turn, c.CreatedAt, now)
	if status != agent.StatusWorking && now.Sub(c.UpdatedAt) >= quietFor {
		return agent.Session{}, false, err
	}
	return agent.Session{
		ID:        string(agent.KindZCode) + ":" + c.ID,
		Kind:      agent.KindZCode,
		Dir:       dir,
		Status:    status,
		Since:     since,
		StartedAt: c.CreatedAt,
	}, true, err
}

// statusOf maps the newest prompt and turn of a conversation to a status and
// when it began. created is when the conversation was opened: with no
// history of its own yet, it waits since then. Nothing here is idle: a
// conversation quiet enough to be idle is not shown at all (see quietFor).
func statusOf(hasInput bool, input Input, hasTurn bool, turn Turn, created, now time.Time) (agent.Status, time.Time) {
	// ZCode starts a turn a few milliseconds after admitting its prompt, so
	// the turn of an earlier prompt began before the newest one: the newest
	// prompt's turn is still running. A prompt this old was never answered —
	// the app died mid-turn, and its turn row will never come.
	if hasInput && now.Sub(input.SentAt) < agent.DormantAfter &&
		(!hasTurn || turn.StartedAt.Before(input.SentAt)) {
		return agent.StatusWorking, input.SentAt
	}
	// The same for a turn ZCode has not closed: it runs only while it is young.
	if hasTurn && turn.Status == "running" && now.Sub(turn.StartedAt) < agent.DormantAfter {
		return agent.StatusWorking, turn.StartedAt
	}
	since := created
	if hasTurn && !turn.CompletedAt.IsZero() {
		since = turn.CompletedAt
	}
	if hasTurn && turn.Status == "error" && !turn.CancelledByUser {
		return agent.StatusError, since
	}
	return agent.StatusWaiting, since
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return path != "" && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
