// Package opencode detects OpenCode sessions.
//
// Every running `opencode` process is a session for its working directory, and
// every tab the TUI has open there is a session of its own. A card's status
// comes from the newest message OpenCode stored for that session.
package opencode

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
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
	// abortedError is the error OpenCode 1 records when the user interrupts a reply.
	abortedError = "MessageAbortedError"
)

// interruptions are the errors that mean the user stopped a reply: the one of
// OpenCode 1, then those of OpenCode 2 (a reply and a compaction).
var interruptions = []string{abortedError, "aborted", "compaction.interrupted"}

// Target is what a store query is about: one session and the sessions it
// spawned, or, when Session is empty, every session of a working directory.
type Target struct {
	Dir     string
	Session string // empty for the whole folder
}

// Message is the metadata of the newest message in a target's sessions.
type Message struct {
	Role      string // "user" or "assistant"
	ErrorName string // empty when the message has no error
	Completed bool   // assistant reply finished
	UpdatedAt time.Time
	// TurnStartedAt is when the prompt this message answers was sent; for a
	// prompt, when it was sent itself.
	TurnStartedAt time.Time
}

// ToolPart is the newest tool call of a target's latest step.
type ToolPart struct {
	State     string // "running", "completed", ...
	UpdatedAt time.Time
}

// Store reads OpenCode's persisted session data.
type Store interface {
	LastMessage(ctx context.Context, target Target) (Message, bool, error)
	LastToolPart(ctx context.Context, target Target) (ToolPart, bool, error)
}

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running OpenCode sessions.
type Source struct {
	Processes ProcessFinder
	Store     Store
	// Tabs reads the sessions the TUI has open in tabs. When it is nil, or has
	// nothing for a folder, that folder is one card for the process in it.
	Tabs Tabs
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindOpenCode) }

// Sessions implements detect.Source. A folder whose history cannot be read
// is still shown, as waiting, and the store errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "opencode")
	if err != nil {
		return nil, err
	}
	// The oldest process in a folder owns it: its card keeps its ID when
	// another opencode starts there, and no reply written since is taken for
	// one left by a previous run (see statusOf). The tabs are the same
	// whichever process of the folder is looked at.
	slices.SortFunc(procs, func(a, b process.Info) int {
		return cmp.Or(a.StartedAt.Compare(b.StartedAt), cmp.Compare(a.PID, b.PID))
	})
	var sessions []agent.Session
	var errs []error
	seen := make(map[string]bool)
	for _, p := range procs {
		if p.Dir == "" || seen[p.Dir] || isServer(p.Args) {
			continue // one look per folder; a server is not a session
		}
		seen[p.Dir] = true
		cards, err := s.cards(ctx, p, now)
		sessions = append(sessions, cards...)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// cards returns the cards of a folder: one per open tab, or a single card for
// the folder when its tabs are not known.
func (s *Source) cards(ctx context.Context, p process.Info, now time.Time) ([]agent.Session, error) {
	tabs, tabsErr := s.openTabs(ctx, p.Dir)
	if len(tabs) == 0 {
		session, err := s.toSession(ctx, Target{Dir: p.Dir}, strconv.Itoa(p.PID), p.StartedAt, now)
		return []agent.Session{session}, errors.Join(tabsErr, err)
	}
	sessions := make([]agent.Session, 0, len(tabs))
	var errs []error
	for i, id := range tabs {
		session, err := s.toSession(ctx, Target{Dir: p.Dir, Session: id}, id, tabStart(p.StartedAt, i), now)
		sessions = append(sessions, session)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// openTabs reads the sessions the TUI has open in dir. No tab state, and a
// state file that cannot be read, both mean one card for the folder: a folder
// is never hidden because its tabs are unknown.
func (s *Source) openTabs(ctx context.Context, dir string) ([]string, error) {
	if s.Tabs == nil {
		return nil, nil
	}
	return s.Tabs.OpenTabs(ctx, dir)
}

// tabStart orders the tabs of one folder. They share their process's start
// time and the tab's place in the strip breaks the tie, so card #1 is the
// first tab.
func tabStart(processStart time.Time, index int) time.Time {
	return processStart.Add(time.Duration(index) * time.Microsecond)
}

// isServer reports `opencode serve`, the background service of OpenCode 2:
// it runs in the home folder for every session and has no session of its own.
// The first argument is the program; flags may come before the command.
func isServer(args []string) bool {
	for _, arg := range args[min(1, len(args)):] {
		if !strings.HasPrefix(arg, "-") {
			return arg == "serve"
		}
	}
	return false
}

// toSession builds the card of a target. suffix is what follows "opencode:" in
// the card ID: the pid of the folder's process, or the tab's session id, which
// the protocol allows and which keeps the card's ID when the tabs are reordered.
func (s *Source) toSession(ctx context.Context, target Target, suffix string, started, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindOpenCode) + ":" + suffix,
		Kind:      agent.KindOpenCode,
		Dir:       target.Dir,
		Status:    agent.StatusWaiting,
		Since:     started,
		StartedAt: started,
	}
	msg, found, err := s.Store.LastMessage(ctx, target)
	if err != nil || !found {
		return session, err
	}
	session.Status = statusOf(msg, started, now)
	session.Since = msg.UpdatedAt
	if session.Status != agent.StatusWorking {
		return session, nil
	}
	blocked, err := s.blockedOnPrompt(ctx, target, now)
	if blocked {
		session.Status = agent.StatusWaiting
	} else {
		session.Since = msg.TurnStartedAt // working since the prompt, like Claude Code
	}
	return session, err
}

// statusOf maps the newest message of a target to a status. started is when
// the opencode process started: a message last written before that belongs
// to a previous run, so it is neither still working nor this run's error.
func statusOf(msg Message, started, now time.Time) agent.Status {
	age := now.Sub(msg.UpdatedAt)
	live := !msg.UpdatedAt.Before(started)
	failed := msg.ErrorName != "" && !slices.Contains(interruptions, msg.ErrorName)
	switch {
	case live && msg.Role == "assistant" && !msg.Completed && msg.ErrorName == "":
		return agent.StatusWorking
	case live && msg.Role == "user" && age < promptWorkingFor:
		return agent.StatusWorking
	case age >= agent.DormantAfter:
		return agent.StatusIdle
	case live && msg.Role == "assistant" && failed:
		return agent.StatusError
	default:
		return agent.StatusWaiting
	}
}

// blockedOnPrompt reports a tool part frozen at "running": OpenCode does not
// record pending permission prompts, but this is their tell.
func (s *Source) blockedOnPrompt(ctx context.Context, target Target, now time.Time) (bool, error) {
	part, found, err := s.Store.LastToolPart(ctx, target)
	return found && part.State == "running" && now.Sub(part.UpdatedAt) > toolStaleAfter, err
}
