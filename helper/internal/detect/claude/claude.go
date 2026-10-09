// Package claude detects Claude Code sessions (terminal and desktop app).
//
// Claude Code keeps a live registry of open sessions in ~/.claude/sessions,
// one <pid>.json per session, including its status. Files left behind by
// crashed sessions are filtered out by checking that the pid is running and
// started when the record says it did.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

// procStartLayout is how Claude Code stores the process start time: the
// output of `ps -o lstart=` in UTC.
const procStartLayout = "Mon Jan _2 15:04:05 2006"

// procStartTolerance absorbs the sub-second precision lost by procStartLayout.
const procStartTolerance = 2 * time.Second

// Context-window sizes of Claude models. Neither the registry nor the
// transcript says which one a session has, so by default it is inferred:
// standard until the usage proves it must be the extended one.
const (
	StandardContextWindow = 200_000
	ExtendedContextWindow = 1_000_000
)

// Source reads the Claude Code session registry.
type Source struct {
	SessionsDir string // ~/.claude/sessions
	ProjectsDir string // ~/.claude/projects (transcripts)
	Processes   process.Table
	// ContextWindow forces the context limit in tokens; 0 infers it.
	ContextWindow int
}

// NewSource returns a Source for the Claude Code installation in home.
func NewSource(home string, processes process.Table) *Source {
	return &Source{
		SessionsDir: filepath.Join(home, ".claude", "sessions"),
		ProjectsDir: filepath.Join(home, ".claude", "projects"),
		Processes:   processes,
	}
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindClaude) }

// Sessions implements detect.Source.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	paths, err := filepath.Glob(filepath.Join(s.SessionsDir, "*.json"))
	if err != nil {
		return nil, err
	}
	var sessions []agent.Session
	for _, path := range paths {
		record, err := readRecord(path)
		if err != nil || !record.isInteractive() || !s.isAlive(ctx, record) {
			continue
		}
		sessions = append(sessions, s.toSession(record, now))
	}
	return sessions, nil
}

// record is the subset of a registry file this package needs.
type record struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	Cwd             string `json:"cwd"`
	StartedAt       int64  `json:"startedAt"` // unix ms
	ProcStart       string `json:"procStart"`
	Status          string `json:"status"`
	WaitingFor      any    `json:"waitingFor"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"` // unix ms
	UpdatedAt       int64  `json:"updatedAt"`       // unix ms
	Spare           bool   `json:"spare"`
	ParkedJobID     string `json:"parkedJobId"`
}

func readRecord(path string) (record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return record{}, err
	}
	var r record
	err = json.Unmarshal(data, &r)
	return r, err
}

// isInteractive excludes pre-spawned spare processes and parked background jobs.
func (r record) isInteractive() bool {
	return r.PID > 0 && !r.Spare && r.ParkedJobID == ""
}

func (r record) isWaitingForUser() bool {
	switch v := r.WaitingFor.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	default:
		return true
	}
}

// changedAt is when the status last changed, falling back to coarser timestamps.
func (r record) changedAt(now time.Time) time.Time {
	for _, ms := range []int64{r.StatusUpdatedAt, r.UpdatedAt, r.StartedAt} {
		if ms > 0 {
			return time.UnixMilli(ms)
		}
	}
	return now
}

func (s *Source) isAlive(ctx context.Context, r record) bool {
	started, running := s.Processes.StartTime(ctx, r.PID)
	if !running {
		return false
	}
	if r.ProcStart == "" {
		return true
	}
	recorded, err := time.ParseInLocation(procStartLayout, strings.Join(strings.Fields(r.ProcStart), " "), time.UTC)
	if err != nil {
		// Unknown format: trust the pid rather than hide a live session.
		return true
	}
	// A different start time means the pid was reused by another process.
	diff := started.Sub(recorded)
	return diff > -procStartTolerance && diff < procStartTolerance
}

func (s *Source) toSession(r record, now time.Time) agent.Session {
	since := r.changedAt(now)
	session := agent.Session{
		ID:        string(agent.KindClaude) + ":" + strconv.Itoa(r.PID),
		Kind:      agent.KindClaude,
		Dir:       r.Cwd,
		Since:     since,
		StartedAt: time.UnixMilli(r.StartedAt),
	}

	switch {
	case r.isWaitingForUser():
		session.Status = agent.StatusWaiting
	case r.Status == "busy":
		session.Status = agent.StatusWorking
	case now.Sub(since) < agent.DormantAfter:
		session.Status = agent.StatusWaiting
	default:
		session.Status = agent.StatusIdle
	}

	if summary, err := s.readTranscript(r.SessionID); err == nil {
		if session.Status != agent.StatusWorking && summary.endsInError {
			session.Status = agent.StatusError
		}
		if summary.context != nil {
			session.Context = &agent.ContextUsage{Used: summary.context.Used, Limit: s.contextLimit(summary.context.Used)}
		}
	}
	return session
}

func (s *Source) contextLimit(used int) int {
	switch {
	case s.ContextWindow > 0:
		return s.ContextWindow
	case used > StandardContextWindow:
		return ExtendedContextWindow
	default:
		return StandardContextWindow
	}
}

func (s *Source) readTranscript(sessionID string) (transcriptSummary, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return transcriptSummary{}, fs.ErrNotExist
	}
	matches, err := filepath.Glob(filepath.Join(s.ProjectsDir, "*", sessionID+".jsonl"))
	if err != nil {
		return transcriptSummary{}, err
	}
	if len(matches) == 0 {
		return transcriptSummary{}, fs.ErrNotExist
	}
	return summarizeTranscript(matches[0])
}

var errNoEntries = errors.New("transcript has no readable entries")
