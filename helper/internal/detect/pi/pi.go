// Package pi detects pi coding agent sessions.
//
// Every running `pi` process is a session for its working directory, and the
// folder's active conversation is the newest session file pi keeps for it:
// <PI_CODING_AGENT_DIR>/sessions/--<encoded cwd>--/<timestamp>_<id>.jsonl. The
// file is read at both ends by the shared pi-format reader (detect/pijsonl):
// the header for the session id, start time and working directory, the end for
// the status. Conversation content — the message contents, compaction and
// branch summaries — is never decoded.
package pi

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect/pijsonl"
	"github.com/tonylook/managents/helper/internal/process"
)

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running pi sessions.
type Source struct {
	Processes ProcessFinder
	// SessionsDir is where pi keeps its session files.
	SessionsDir string
}

// NewSource returns a Source for the pi installation of the user whose home
// directory is home: $PI_CODING_AGENT_SESSION_DIR when it is set, otherwise
// the sessions folder of $PI_CODING_AGENT_DIR (default ~/.pi/agent).
func NewSource(home string, processes ProcessFinder) *Source {
	dir := os.Getenv("PI_CODING_AGENT_SESSION_DIR")
	if dir == "" {
		root := os.Getenv("PI_CODING_AGENT_DIR")
		if root == "" {
			root = filepath.Join(home, ".pi", "agent")
		}
		dir = filepath.Join(root, "sessions")
	}
	return &Source{Processes: processes, SessionsDir: dir}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindPi) }

// Sessions implements detect.Source. A folder whose session files cannot be
// read is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "pi")
	if err != nil {
		return nil, err
	}
	// The oldest process in a folder owns its card, as for Codex.
	slices.SortFunc(procs, func(a, b process.Info) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.PID, b.PID))
	})
	var sessions []agent.Session
	var errs []error
	seen := make(map[string]bool)
	for _, p := range procs {
		if p.Dir == "" || seen[p.Dir] {
			continue // one look per folder
		}
		seen[p.Dir] = true
		session, err := s.toSession(p, now)
		sessions = append(sessions, session)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// toSession builds the card of a folder: the newest session file pi has for
// it, or a waiting card when pi has written none there yet.
func (s *Source) toSession(p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindPi) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindPi,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	path, h, err := s.activeSession(p.Dir)
	if err != nil {
		return session, err
	}
	if path == "" {
		return session, nil // pi writes the session file with the first message
	}
	summary, err := pijsonl.Summarize(path)
	if err != nil {
		return session, err
	}
	if h.SessionID != "" {
		session.ID = string(agent.KindPi) + ":" + h.SessionID
	} else {
		session.ID = string(agent.KindPi) + ":" + strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if !h.Timestamp.IsZero() {
		session.StartedAt = h.Timestamp
	}
	session.Context = summary.Context
	session.Status, session.Since = pijsonl.StatusOf(summary, p.StartedAt, now)
	return session, nil
}

// activeSession returns the folder's active session file: the newest one in
// the session folder pi names after the encoded working directory, whose
// header carries that directory. The folder is matched by name, not by a
// pattern: an encoded name keeps the punctuation of the path, which a glob
// would read as wildcards. The path is empty when there is none.
func (s *Source) activeSession(dir string) (string, pijsonl.Head, error) {
	return pijsonl.NewestSessionFile(s.SessionsDir, dir, []string{sessionDir(dir)})
}

// sessionDir is what pi names a working directory's session folder: the path
// with its leading slash stripped and / \ : turned into '-', wrapped in double
// dashes.
func sessionDir(dir string) string {
	encoded := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(dir, "/"))
	return "--" + encoded + "--"
}
