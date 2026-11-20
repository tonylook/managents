// Package amp detects Sourcegraph Amp threads.
//
// Amp's system of record is ampcode.com, and the only local trace of a thread
// is a mirror: one JSON file per thread at <data dir>/threads/T-<id>.json —
// macOS and Linux alike: $XDG_DATA_HOME/amp/threads, default
// ~/.local/share/amp/threads — rewritten in place as the thread runs. The
// mirror is undocumented and not guaranteed to be there, so nothing here is
// certain: a thread the mirror does not carry (one run by the desktop app, an
// orb or a runner) gets no card, and a thread whose working directory the
// mirror does not name gets none either — the directory lives in the opaque
// env object the thread captures at its start, and which key carries it could
// not be confirmed, so every plausible one is read and only an absolute path
// is trusted.
//
// The statuses themselves are explicit: a tool result run is queued,
// in-progress, blocked-on-user (or rejected by the user), done, cancelled or
// errored, and a message is streaming or complete, with a stop reason. A
// thread is a card while amp writes it — like ZCode's conversations, a thread
// the mirror has been quiet on is treated as closed or abandoned — while its
// turn runs (kept whatever the write age, for a run that died mid-turn stops
// being shown after 2 h of silence) and while it waits on the user, whose
// answer is the next write. Conversation content — the text, the thinking,
// the tool calls and their results, the titles — is never decoded.
package amp

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
)

// quietFor is how long a thread the mirror has not been rewritten for stops
// being a card: amp rewrites the file on every event, so a quiet thread is
// one that waits for its user, whose answer is the next write — but only the
// threads of the last few minutes are still of interest, and amp records no
// close. A thread whose turn is running, or that waits on the user, is kept
// whatever its write age.
const quietFor = 5 * time.Minute

// Source detects Amp threads through their mirror.
type Source struct {
	// ThreadsDir is where amp mirrors its threads: <data dir>/threads.
	ThreadsDir string
}

// NewSource returns a Source for the amp installation of the user whose home
// directory is home.
func NewSource(home string) *Source {
	return &Source{ThreadsDir: DefaultThreadsDir(home)}
}

// DefaultThreadsDir is where amp mirrors its threads for the user whose home
// directory is home: $XDG_DATA_HOME/amp/threads, default ~/.local/share.
func DefaultThreadsDir(home string) string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "amp", "threads")
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindAmp) }

// Sessions implements detect.Source. A thread that cannot be read is an
// error, returned alongside the cards of the threads that can.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	entries, err := os.ReadDir(s.ThreadsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // amp has mirrored no threads here
		}
		return nil, err
	}
	var sessions []agent.Session
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		session, shown, err := s.toSession(filepath.Join(s.ThreadsDir, entry.Name()), now)
		if err != nil {
			errs = append(errs, err)
		}
		if shown {
			sessions = append(sessions, session)
		}
	}
	return sessions, errors.Join(errs...)
}

// toSession reads one thread mirror. shown is false for a thread that gets no
// card: one the mirror does not name a working directory for, one that has
// been closed or abandoned, or one whose run died mid-turn.
func (s *Source) toSession(path string, now time.Time) (agent.Session, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return agent.Session{}, false, err
	}
	written := info.ModTime()
	thread, err := readThread(path)
	if err != nil {
		return agent.Session{}, false, err
	}
	dir := thread.Env.workingDir()
	if dir == "" {
		return agent.Session{}, false, nil // no trustworthy working directory: not a card
	}
	signal := signalOf(thread)
	switch {
	case signal.running && now.Sub(written) >= agent.DormantAfter:
		return agent.Session{}, false, nil // a run that died mid-turn
	case !signal.running && !signal.blocked && now.Sub(written) >= quietFor:
		return agent.Session{}, false, nil // closed or abandoned: amp wrote nothing since
	}
	session := agent.Session{
		ID:     string(agent.KindAmp) + ":" + thread.ID,
		Kind:   agent.KindAmp,
		Dir:    dir,
		Status: agent.StatusWaiting,
		Since:  written,
	}
	if created := unixAt(thread.Created); !created.IsZero() {
		session.StartedAt = created
	} else {
		session.StartedAt = written
	}
	session.Context = contextOf(thread)
	session.Status, session.Since = statusOf(signal, written)
	return session, true, nil
}

// readThread reads a thread mirror, one JSON document. Only the fields below
// are decoded; a parse error is a broken mirror, and an error.
func readThread(path string) (thread, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return thread{}, err
	}
	var parsed thread
	if err := json.Unmarshal(data, &parsed); err != nil {
		return thread{}, err
	}
	if parsed.ID == "" {
		parsed.ID = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	return parsed, nil
}

// thread declares only the fields we use. The title amp infers from the
// conversation, the message contents and the environment context are never
// decoded into memory we keep, logged or sent anywhere.
type thread struct {
	ID       string    `json:"id"`
	Created  int64     `json:"created"` // unix milliseconds
	Env      env       `json:"env"`
	Messages []message `json:"messages"`
}

// message declares only the metadata fields we use. The content blocks' text,
// thinking, tool inputs and results are never decoded into memory we keep:
// only the type of each block and the status of a tool run are.
type message struct {
	Role    string    `json:"role"` // user, assistant, info
	Content []content `json:"content"`
	State   *struct {
		Type       string `json:"type"`       // streaming or complete
		StopReason string `json:"stopReason"` // end_turn, tool_use, error, ...
	} `json:"state"`
	Usage *struct {
		TotalInputTokens int `json:"totalInputTokens"`
	} `json:"usage"`
}

// content declares only the block type and the status of a tool run.
type content struct {
	Type string `json:"type"` // text, thinking, redacted_thinking, tool_use, tool_result, ...
	Run  *struct {
		Status string `json:"status"` // done, error, cancelled, rejected-by-user, blocked-on-user, queued, in-progress
	} `json:"run"`
}

// The run statuses that decide what a turn is doing while their block is the
// newest one: still to run or running, waiting for the user's answer (or
// refused by them), or failed. done and cancelled are settled runs.
const (
	runQueued        = "queued"
	runInProgress    = "in-progress"
	runBlockedOnUser = "blocked-on-user"
	runRejected      = "rejected-by-user"
	runFailed        = "error"
)

// env is the environment context a thread captures at its start, opaque
// beyond the working directory it may name. Which key carries it could not be
// confirmed, so every plausible one is read, and only an absolute path is
// trusted.
type env struct {
	Cwd     string `json:"cwd"`
	Initial struct {
		Cwd              string `json:"cwd"`
		Pwd              string `json:"pwd"`
		WorkingDir       string `json:"workingDir"`
		WorkingDirectory string `json:"workingDirectory"`
		Directory        string `json:"directory"`
	} `json:"initial"`
}

// workingDir returns the thread's working directory: the first candidate key
// that holds an absolute path. Empty when none does.
func (e env) workingDir() string {
	for _, candidate := range []string{
		e.Cwd, e.Initial.Cwd, e.Initial.Pwd, e.Initial.WorkingDir,
		e.Initial.WorkingDirectory, e.Initial.Directory,
	} {
		if filepath.IsAbs(candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

// signal is what the newest message of a thread says it is doing.
type signal struct {
	running bool // the turn is being written, or a tool of it queued or in progress
	blocked bool // a tool waits for the user's answer, or was refused by them
	failed  bool // the newest run failed
}

// signalOf reads what a thread's newest message says. The newest content
// block decides: a tool result still queued or in progress is a turn being
// run, one that waits for the user's answer (or that the user refused) is
// blocked on the user, a failed one an error. Without a settled tool run the
// message's own state decides: a message still streaming is a turn being
// written, a complete one that stopped on an error a failure, any other a
// turn that is over.
func signalOf(thread thread) signal {
	var sig signal
	if len(thread.Messages) == 0 {
		return sig
	}
	newest := thread.Messages[len(thread.Messages)-1]
	if len(newest.Content) > 0 {
		run := newest.Content[len(newest.Content)-1].Run
		switch {
		case run == nil:
		case run.Status == runQueued || run.Status == runInProgress:
			sig.running = true
			return sig
		case run.Status == runBlockedOnUser || run.Status == runRejected:
			sig.blocked = true
			return sig
		case run.Status == runFailed:
			sig.failed = true
			return sig
		}
	}
	switch {
	case newest.State == nil:
	case newest.State.Type == "streaming":
		sig.running = true
	case newest.State.StopReason == "error":
		sig.failed = true
	}
	return sig
}

// statusOf maps a thread's signal to a status and when it began: the last
// write of the mirror is the closest thing to it, the file being rewritten on
// every event. A running turn works, a blocked one waits for as long as it
// takes — the user's answer is the next write — and a failure shows the
// error.
func statusOf(sig signal, written time.Time) (agent.Status, time.Time) {
	switch {
	case sig.running:
		return agent.StatusWorking, written
	case sig.failed:
		return agent.StatusError, written
	default:
		return agent.StatusWaiting, written
	}
}

// contextOf returns the newest assistant usage: totalInputTokens counts the
// whole prompt of that turn, its input, its cache creation and its cache
// reads alike. amp stores no context-window size, so there is no limit.
func contextOf(thread thread) *agent.ContextUsage {
	for i := len(thread.Messages) - 1; i >= 0; i-- {
		message := thread.Messages[i]
		if message.Role == "assistant" && message.Usage != nil && message.Usage.TotalInputTokens > 0 {
			return &agent.ContextUsage{Used: message.Usage.TotalInputTokens}
		}
	}
	return nil
}

// unixAt converts the thread's creation, unix milliseconds, to a time, zero
// when it is empty.
func unixAt(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}
