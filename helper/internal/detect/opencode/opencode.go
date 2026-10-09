// Package opencode detects OpenCode sessions.
//
// Every running `opencode` process is a session for its working directory.
// Its status comes from the newest message OpenCode stored for that directory.
package opencode

import (
	"context"
	"strconv"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

const (
	// promptWorkingFor is how long a just-sent prompt counts as working before
	// the assistant's reply shows up in the store.
	promptWorkingFor = 90 * time.Second
	// toolStaleAfter: a tool stuck at "running" this long without an update is
	// waiting on a permission or question prompt (real tools keep updating).
	toolStaleAfter = 6 * time.Second
	// abortedError is the error OpenCode records when the user interrupts a reply.
	abortedError = "MessageAbortedError"
)

// Message is the metadata of the newest message in a directory's sessions.
type Message struct {
	Role      string // "user" or "assistant"
	ErrorName string // empty when the message has no error
	Completed bool   // assistant reply finished
	UpdatedAt time.Time
}

// ToolPart is the newest tool call of a directory's latest session.
type ToolPart struct {
	State     string // "running", "completed", ...
	UpdatedAt time.Time
}

// Store reads OpenCode's persisted session data.
type Store interface {
	LastMessage(ctx context.Context, dir string) (Message, bool, error)
	LastToolPart(ctx context.Context, dir string) (ToolPart, bool, error)
}

// Source detects running OpenCode sessions.
type Source struct {
	Processes process.Table
	Store     Store
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindOpenCode) }

// Sessions implements detect.Source.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "opencode")
	if err != nil {
		return nil, err
	}
	var sessions []agent.Session
	seen := make(map[string]bool)
	for _, p := range procs {
		if p.Dir == "" || seen[p.Dir] {
			continue // one card per folder, like the POC
		}
		seen[p.Dir] = true
		sessions = append(sessions, s.toSession(ctx, p, now))
	}
	return sessions, nil
}

func (s *Source) toSession(ctx context.Context, p process.Info, now time.Time) agent.Session {
	session := agent.Session{
		ID:        string(agent.KindOpenCode) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindOpenCode,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	msg, found, err := s.Store.LastMessage(ctx, p.Dir)
	if err != nil || !found {
		return session
	}
	session.Since = msg.UpdatedAt
	session.Status = statusOf(msg, now)
	if session.Status == agent.StatusWorking && s.blockedOnPrompt(ctx, p.Dir, now) {
		session.Status = agent.StatusWaiting
	}
	return session
}

func statusOf(msg Message, now time.Time) agent.Status {
	age := now.Sub(msg.UpdatedAt)
	failed := msg.ErrorName != "" && msg.ErrorName != abortedError
	switch {
	case msg.Role == "assistant" && failed:
		return agent.StatusError
	case msg.Role == "assistant" && !msg.Completed && msg.ErrorName == "":
		return agent.StatusWorking
	case msg.Role == "user" && age < promptWorkingFor:
		return agent.StatusWorking
	case age >= agent.DormantAfter:
		return agent.StatusIdle
	default:
		return agent.StatusWaiting
	}
}

// blockedOnPrompt reports a tool part frozen at "running": OpenCode does not
// record pending permission prompts, but this is their tell.
func (s *Source) blockedOnPrompt(ctx context.Context, dir string, now time.Time) bool {
	part, found, err := s.Store.LastToolPart(ctx, dir)
	return err == nil && found && part.State == "running" && now.Sub(part.UpdatedAt) > toolStaleAfter
}
