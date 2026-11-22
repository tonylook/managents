// Package qwen detects Qwen Code sessions.
//
// Every running `qwen` process is a session for its working directory, and the
// folder's active conversation is the newest session file Qwen Code keeps for
// it: <QWEN_HOME>/projects/<sanitized cwd>/chats/<session id>.jsonl, an
// append-only JSON-lines log. Its records carry the working directory, token
// counts and the context-window size, and every turn ends with an explicit
// turn_result record. Conversation content — the message field, the turn
// result texts, the title sidecars — is never decoded.
package qwen

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
// the last turn result or token count.
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

// Source detects running Qwen Code sessions.
type Source struct {
	Processes ProcessFinder
	// Root is the Qwen Code directory: ~/.qwen, or $QWEN_HOME when it is set.
	Root string
}

// NewSource returns a Source for the Qwen Code installation of the user whose
// home directory is home: $QWEN_HOME when it is set, otherwise ~/.qwen.
func NewSource(home string, processes ProcessFinder) *Source {
	dir := os.Getenv("QWEN_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".qwen")
	}
	return &Source{Processes: processes, Root: dir}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindQwen) }

// Sessions implements detect.Source. A folder whose session files cannot be
// read is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "qwen")
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

// toSession builds the card of a folder: the newest session file Qwen has for
// it, or a waiting card when Qwen has written none there yet.
func (s *Source) toSession(p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindQwen) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindQwen,
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
		return session, nil // Qwen writes the session file with the first prompt
	}
	summary, err := summarizeSession(path)
	if err != nil {
		return session, err
	}
	if h.SessionID != "" {
		session.ID = string(agent.KindQwen) + ":" + h.SessionID
	}
	if !h.Timestamp.IsZero() {
		session.StartedAt = h.Timestamp
	}
	session.Context = summary.context
	session.Status, session.Since = statusOf(summary, p.StartedAt, now)
	return session, nil
}

// activeSession returns the folder's active session file: the newest one in
// the project directory Qwen Code names after the sanitized working directory,
// whose records carry that directory. The path is empty when there is none.
func (s *Source) activeSession(dir string) (string, head, error) {
	matches, err := filepath.Glob(filepath.Join(s.Root, "projects", sanitizeCwd(dir), "chats", "*.jsonl"))
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
			continue // another folder that sanitizes to the same name
		}
		if best == "" || info.ModTime().After(bestMod) {
			best, bestHead, bestMod = path, h, info.ModTime()
		}
	}
	return best, bestHead, nil
}

// sanitizeCwd is how Qwen Code names a project's directory: every
// non-alphanumeric character of the working directory becomes '-'.
func sanitizeCwd(dir string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, dir)
}

// head is what the start of a session file says about its session.
type head struct {
	SessionID string
	Timestamp time.Time // when the session was opened
	Cwd       string
}

// readHead reads the start of a session file. ok is false when it does not
// name a working directory, so the file cannot be tied to a folder.
func readHead(path string) (h head, ok bool, err error) {
	f, err := detect.OpenRegular(path)
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
		var e headRecord
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &e) != nil {
			continue
		}
		if h.Timestamp.IsZero() {
			h.Timestamp = e.Timestamp
		}
		if h.SessionID == "" {
			h.SessionID = e.SessionID
		}
		if h.Cwd == "" {
			h.Cwd = e.Cwd
		}
	}
	return h, h.Cwd != "", nil
}

// headRecord declares only the head fields we use. Message content is never
// decoded into memory we keep, logged or sent anywhere.
type headRecord struct {
	SessionID string    `json:"sessionId"`
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
	turn    turnState
	at      time.Time // when the newest turn started (running) or ended (ended)
	failed  bool      // the newest turn ended in an error
	context *agent.ContextUsage
}

// entry declares only the metadata fields we use. The message field, the turn
// result texts and tool outputs are never decoded into memory we keep, logged
// or sent anywhere.
type entry struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	Subtype     string    `json:"subtype"`
	State       string    `json:"state"`   // turn_result: completed, cancelled or error
	EndedAt     time.Time `json:"endedAt"` // turn_result
	IsSidechain bool      `json:"isSidechain"`
	Usage       *struct {
		TotalTokenCount int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ContextWindowSize int `json:"contextWindowSize"`
}

// endedAt is when the turn ended, falling back to the record's timestamp.
func (e entry) endedAt() time.Time {
	if !e.EndedAt.IsZero() {
		return e.EndedAt
	}
	return e.Timestamp
}

// isConversation excludes subagent traffic and the bookkeeping records
// (notifications, compressions, titles, model switches) around a turn.
func (e entry) isConversation() bool {
	return !e.IsSidechain && (e.Subtype == "turn_result" || e.Type == "user")
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

// summarize scans the lines of tail backwards. The newest conversation record
// decides where the turn stands: a turn_result is an ended turn, a user
// message a prompt whose turn has not ended yet. The context comes from the
// newest token count and context-window size, which ride on the same records.
func summarize(tail []byte) (sessionSummary, bool) {
	var summary sessionSummary
	decided := false
	for rest := tail; len(rest) > 0 && !(decided && summary.context != nil); {
		i := bytes.LastIndexByte(rest, '\n')
		line := rest[i+1:]
		rest = rest[:max(i, 0)]

		var e entry
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &e) != nil {
			continue
		}
		if summary.context == nil && !e.IsSidechain && e.Usage != nil && e.Usage.TotalTokenCount > 0 {
			summary.context = &agent.ContextUsage{Used: e.Usage.TotalTokenCount, Limit: e.ContextWindowSize}
		}
		if decided || !e.isConversation() {
			continue
		}
		switch {
		case e.Subtype == "turn_result":
			decided = true
			summary.turn, summary.at, summary.failed = turnEnded, e.endedAt(), e.State == "error"
		case e.Type == "user":
			decided = true
			summary.turn, summary.at = turnRunning, e.Timestamp
		}
	}
	return summary, decided && summary.context != nil
}

// statusOf maps the newest turn of a session to a status and when it began.
// started is when the qwen process started: a record written before that
// belongs to a previous run, so it is neither still working nor this run's
// error, and the session waits since it was opened. A prompt whose turn has
// not ended works only while it is young: one this old is a run that died
// mid-turn.
func statusOf(summary sessionSummary, started, now time.Time) (agent.Status, time.Time) {
	live := !summary.at.Before(started)
	since := summary.at
	if !live {
		since = started
	}
	switch {
	case summary.turn == noTurn:
		return agent.StatusWaiting, since
	case live && summary.turn == turnRunning && now.Sub(since) < agent.DormantAfter:
		return agent.StatusWorking, since
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	case live && summary.failed:
		return agent.StatusError, since
	default:
		return agent.StatusWaiting, since
	}
}
