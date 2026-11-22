// Package gemini detects Gemini CLI sessions.
//
// Every running `gemini` process is a session for its working directory, and
// the folder's active conversation is the newest session file Gemini CLI keeps
// for its project: <GEMINI_DIR>/tmp/<slug>/chats/session-<timestamp>-<id>.jsonl,
// an append-only JSON-lines log. The project's slug comes from the registry,
// projects.json, or from the .project_root marker files Gemini CLI leaves in
// its tmp directories; a subagent session (kind subagent, nested one level
// down) gets no card of its own, like Codex's subagent threads. Conversation
// content — the message content, display content, tool calls, thoughts and
// summaries — is never decoded.
package gemini

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
// session id and start time.
const headBytes = 64 << 10

// The tool-call statuses that say what a turn is doing while its record is the
// newest one: awaiting approval from the user, or running. The other statuses
// (validating, scheduled, success, error, cancelled) are settled calls.
const (
	toolAwaitingApproval = "awaiting_approval"
	toolExecuting        = "executing"
)

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running Gemini CLI sessions.
type Source struct {
	Processes ProcessFinder
	// Root is the Gemini CLI directory: ~/.gemini.
	Root string
}

// NewSource returns a Source for the Gemini CLI installation of the user whose
// home directory is home.
func NewSource(home string, processes ProcessFinder) *Source {
	return &Source{Processes: processes, Root: filepath.Join(home, ".gemini")}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindGemini) }

// Sessions implements detect.Source. A folder whose session files cannot be
// read is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "gemini")
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

// toSession builds the card of a folder: the newest session file of its
// project, or a waiting card when Gemini CLI has written none there yet.
func (s *Source) toSession(p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindGemini) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindGemini,
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
		return session, nil // Gemini CLI writes the session file with the first message
	}
	summary, err := summarizeSession(path)
	if err != nil {
		return session, err
	}
	if h.SessionID != "" {
		session.ID = string(agent.KindGemini) + ":" + h.SessionID
	} else {
		session.ID = string(agent.KindGemini) + ":" + strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	if !h.StartTime.IsZero() {
		session.StartedAt = h.StartTime
	}
	session.Context = summary.context
	session.Status, session.Since = statusOf(summary, p.StartedAt, now)
	return session, nil
}

// activeSession returns the folder's active session file: the newest one in
// the chats directory of the project's slug. The slug comes from the registry,
// projects.json, or from the .project_root markers of the tmp directories. The
// path is empty when there is none, or when the file is not a main session.
func (s *Source) activeSession(dir string) (string, head, error) {
	var best string
	var bestHead head
	var bestMod time.Time
	for _, slug := range s.slugs(dir) {
		matches, err := filepath.Glob(filepath.Join(s.Root, "tmp", slug, "chats", "session-*.jsonl"))
		if err != nil {
			return "", head{}, err
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			h, ok, err := readHead(path)
			if err != nil {
				return "", head{}, err
			}
			if !ok || h.Kind == "subagent" {
				continue // a subagent session is not a card of its own
			}
			if best == "" || info.ModTime().After(bestMod) {
				best, bestHead, bestMod = path, h, info.ModTime()
			}
		}
	}
	return best, bestHead, nil
}

// slugs lists the tmp-directory names Gemini CLI may keep the project's
// sessions in: the one the registry maps the working directory to, then the
// ones whose .project_root marker claims it.
func (s *Source) slugs(dir string) []string {
	var slugs []string
	if data, err := detect.ReadFileBounded(filepath.Join(s.Root, "projects.json")); err == nil {
		var registry projectsFile
		if json.Unmarshal(data, &registry) == nil && registry.Projects[dir] != "" {
			slugs = append(slugs, registry.Projects[dir])
		}
	}
	markers, _ := filepath.Glob(filepath.Join(s.Root, "tmp", "*", ".project_root"))
	for _, marker := range markers {
		data, err := detect.ReadFileBounded(marker)
		if err != nil {
			continue
		}
		if claim := strings.Trim(string(data), "\" \n\r\t"); claim == dir {
			slug := filepath.Base(filepath.Dir(marker))
			if !slices.Contains(slugs, slug) {
				slugs = append(slugs, slug)
			}
		}
	}
	return slugs
}

// projectsFile is the registry of projects Gemini CLI has run in: their
// working directory mapped to the slug of their tmp directory.
type projectsFile struct {
	Projects map[string]string `json:"projects"`
}

// head is what the start of a session file says about its session.
type head struct {
	SessionID string
	StartTime time.Time
	Kind      string // "main" or "subagent"
}

// readHead reads the start of a session file, whose first record is the
// session's metadata. ok is false when it does not name a session.
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
	for rest := data; len(rest) > 0 && (h.SessionID == "" || h.StartTime.IsZero() || h.Kind == ""); {
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
		if h.SessionID == "" {
			h.SessionID = r.SessionID
		}
		if h.StartTime.IsZero() {
			h.StartTime = r.StartTime
		}
		if h.Kind == "" {
			h.Kind = r.Kind
		}
	}
	return h, h.SessionID != "", nil
}

// headRecord declares only the head fields we use. The summary and the message
// list are never decoded into memory we keep, logged or sent anywhere.
type headRecord struct {
	SessionID string    `json:"sessionId"`
	StartTime time.Time `json:"startTime"`
	Kind      string    `json:"kind"`
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

// entry declares only the metadata fields we use. The content, display
// content, tool call arguments and results, thoughts and summary are never
// decoded into memory we keep, logged or sent anywhere.
type entry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // user, gemini, info, error, warning
	Tokens    *struct {
		Total int `json:"total"`
	} `json:"tokens"`
	ToolCalls []struct {
		Status string `json:"status"`
	} `json:"toolCalls"`
}

// executing reports a record whose tool calls are still running: while it is
// the newest record, the turn is still working.
func (e entry) executing() bool {
	for _, call := range e.ToolCalls {
		if call.Status == toolExecuting {
			return true
		}
	}
	return false
}

// awaitingApproval reports a record whose tool calls wait for the user's
// approval: the tell of a permission prompt.
func (e entry) awaitingApproval() bool {
	for _, call := range e.ToolCalls {
		if call.Status == toolAwaitingApproval {
			return true
		}
	}
	return false
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

// summarize scans the lines of tail backwards. The newest message record
// decides where the turn stands: a user message a prompt whose reply has not
// arrived, a gemini message the reply (its tool calls say whether the turn
// went on), an error record an error. The context comes from the newest token
// count. Gemini CLI writes no metadata updates ($set) that decide the status.
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
		if summary.context == nil && e.Tokens != nil && e.Tokens.Total > 0 {
			summary.context = &agent.ContextUsage{Used: e.Tokens.Total}
		}
		if decided {
			continue
		}
		switch e.Type {
		case "gemini":
			decided = true
			summary.at = e.Timestamp
			if !e.awaitingApproval() && e.executing() {
				summary.turn = turnRunning
			} else {
				summary.turn = turnEnded
			}
		case "user":
			decided = true
			summary.turn, summary.at = turnRunning, e.Timestamp
		case "error":
			decided = true
			summary.turn, summary.at, summary.failed = turnEnded, e.Timestamp, true
		}
	}
	return summary, decided && summary.context != nil
}

// statusOf maps the newest turn of a session to a status and when it began.
// started is when the gemini process started: a record written before that
// belongs to a previous run, so it is neither still working nor this run's
// error, and the session waits since it was opened. A prompt whose reply has
// not arrived works only while it is young: one this old is a run that died
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
