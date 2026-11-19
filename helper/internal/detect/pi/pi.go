// Package pi detects pi coding agent sessions.
//
// Every running `pi` process is a session for its working directory, and the
// folder's active conversation is the newest session file pi keeps for it:
// <PI_CODING_AGENT_DIR>/sessions/--<encoded cwd>--/<timestamp>_<id>.jsonl, an
// append-only JSON-lines log whose first line is the session header and whose
// message entries carry the role, the stop reason and the token counts of each
// model call. pi records no turn state, so the end of the log decides: a
// prompt without its reply is a turn in progress, an assistant stop is a
// finished one, and an assistant message that stopped to call a tool with no
// tool result after it is a permission prompt. Conversation content — the
// message contents, compaction and branch summaries — is never decoded.
package pi

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/process"
)

// Only the end of a session file is scanned: sessionTail bytes, growing
// fourfold up to maxSessionTail when a huge entry (a large tool result) hides
// the last message or token count.
const (
	sessionTail    = 256 << 10
	maxSessionTail = 4 << 20
)

// headBytes bounds how much of the start of a session file is read to find its
// session id, start time and working directory.
const headBytes = 64 << 10

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
	summary, err := summarizeSession(path)
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
	session.Context = summary.context
	session.Status, session.Since = statusOf(summary, p.StartedAt, now)
	return session, nil
}

// activeSession returns the folder's active session file: the newest one in
// the session folder pi names after the encoded working directory, whose
// header carries that directory. The path is empty when there is none.
func (s *Source) activeSession(dir string) (string, head, error) {
	matches, err := filepath.Glob(filepath.Join(s.SessionsDir, sessionDir(dir), "*.jsonl"))
	if err != nil {
		return "", head{}, err
	}
	var (
		best     string
		bestHead head
		bestMod  time.Time
	)
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		h, ok, err := readHead(path)
		if err != nil {
			return "", head{}, err
		}
		if !ok || h.Cwd != dir {
			continue // another folder that encodes to the same name
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestHead, bestMod = path, h, info.ModTime()
		}
	}
	return best, bestHead, nil
}

// sessionDir is what pi names a working directory's session folder: the path
// with its leading slash stripped and / \ : turned into '-', wrapped in double
// dashes.
func sessionDir(dir string) string {
	encoded := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(dir, "/"))
	return "--" + encoded + "--"
}

// head is what the header of a session file says about its session.
type head struct {
	SessionID string
	Timestamp time.Time // when the session was opened
	Cwd       string
}

// readHead reads the start of a session file, whose first line is the session
// header. ok is false when it does not name a working directory, so the file
// cannot be tied to a folder.
func readHead(path string) (h head, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return head{}, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err != nil {
		return head{}, false, err
	}
	for rest := data; len(rest) > 0 && (h.SessionID == "" || h.Timestamp.IsZero() || h.Cwd == ""); {
		i := bytes.IndexByte(rest, '\n')
		line := rest
		if i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = nil
		}
		var r headRecord
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		if h.Timestamp.IsZero() {
			h.Timestamp = r.Timestamp
		}
		if h.SessionID == "" {
			h.SessionID = r.SessionID
		}
		if h.Cwd == "" {
			h.Cwd = r.Cwd
		}
	}
	return h, h.Cwd != "", nil
}

// headRecord declares only the header fields we use. The parent session path
// and everything after the header are never decoded beyond the fields below.
type headRecord struct {
	SessionID string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Cwd       string    `json:"cwd"`
}

// turnState is where the newest turn of a session is.
type turnState int

const (
	noTurn turnState = iota
	turnRunning
	turnEnded
)

// sessionSummary is what the end of a session file says about its conversation.
type sessionSummary struct {
	turn     turnState
	at       time.Time // when the newest turn started (running) or ended (ended)
	promptAt time.Time // when the newest user message was sent
	failed   bool      // the newest turn ended in an error
	context  *agent.ContextUsage
}

// entry declares only the metadata fields we use. The message contents, the
// compaction and branch summaries and tool results are never decoded into
// memory we keep, logged or sent anywhere.
type entry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // session, message, model_change, usage, ...
	Message   *struct {
		Role       string `json:"role"` // user, assistant, toolResult, system
		StopReason string `json:"stopReason"`
		Usage      *struct {
			TotalTokens int `json:"totalTokens"`
		} `json:"usage"`
	} `json:"message"`
}

// summarizeSession reads the end of a session file (JSON lines).
func summarizeSession(path string) (sessionSummary, error) {
	for size := int64(sessionTail); ; size *= 4 {
		tail, whole, err := detect.ReadTail(path, size)
		if err != nil {
			return sessionSummary{}, err
		}
		summary, complete := summarize(tail)
		if complete || whole || size >= maxSessionTail {
			return summary, nil
		}
	}
}

// summarize scans the lines of tail backwards. The newest message entry
// decides where the turn stands: a user message or a tool result is a turn in
// progress, an assistant message the model's last word — stopped to call a
// tool (its tool result will come once the user approves, so it is a
// permission prompt), stopped with an error, or stopped for good. The
// timestamp of the newest user message dates the running turn, and the newest
// token count gives the context.
func summarize(tail []byte) (sessionSummary, bool) {
	var summary sessionSummary
	decided := false
	for rest := tail; len(rest) > 0 && !(decided && summary.context != nil && !summary.promptAt.IsZero()); {
		i := bytes.LastIndexByte(rest, '\n')
		line := rest[i+1:]
		rest = rest[:max(i, 0)]

		var e entry
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &e) != nil || e.Message == nil {
			continue
		}
		if summary.context == nil && e.Message.Usage != nil && e.Message.Usage.TotalTokens > 0 {
			summary.context = &agent.ContextUsage{Used: e.Message.Usage.TotalTokens}
		}
		if summary.promptAt.IsZero() && e.Message.Role == "user" {
			summary.promptAt = e.Timestamp
		}
		if decided {
			continue
		}
		switch e.Message.Role {
		case "user", "toolResult":
			decided = true
			summary.turn, summary.at = turnRunning, e.Timestamp
		case "assistant":
			decided = true
			summary.at = e.Timestamp
			switch e.Message.StopReason {
			case "pending":
				summary.turn = turnRunning // the model is still answering
			case "error":
				summary.turn, summary.failed = turnEnded, true
			default: // stop, toolUse, aborted, length, deferred
				summary.turn = turnEnded
			}
		}
	}
	return summary, decided
}

// statusOf maps the newest turn of a session to a status and when it began.
// started is when the pi process started: an entry written before that belongs
// to a previous run, so it is neither still working nor this run's error, and
// the session waits since it was opened. A prompt whose reply has not arrived
// works only while it is young: one this old is a run that died mid-turn.
func statusOf(summary sessionSummary, started, now time.Time) (agent.Status, time.Time) {
	live := !summary.at.Before(started)
	since := summary.at
	if !live {
		since = started
	}
	switch {
	case summary.turn == noTurn:
		return agent.StatusWaiting, since
	case live && summary.turn == turnRunning:
		if !summary.promptAt.IsZero() {
			since = summary.promptAt
		}
		if now.Sub(since) < agent.DormantAfter {
			return agent.StatusWorking, since
		}
		return agent.StatusIdle, since
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	case live && summary.failed:
		return agent.StatusError, since
	default:
		return agent.StatusWaiting, since
	}
}
