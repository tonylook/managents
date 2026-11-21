// Package crush detects Charm Crush sessions.
//
// Every running `crush` process is a session for its working directory, and
// the folder's state lives in a per-project data directory: the `.crush`
// directory Crush finds at or above the working directory, or the one its
// global registry, projects.json, maps the directory to. The database there,
// crush.db, is opened read-only for the project's active session (its newest
// one, without the special sessions Crush runs for itself: title generation
// and agent-tool sessions) and for that session's newest message, whose
// finished_at says whether the turn is over. Crush holds a lock file,
// crush.lock, over the data directory while it runs; a lock whose pid is not
// running is one a crashed run left behind, and one whose pid is not the
// card's own process's is not holding it either — a repository can commit any
// lock file — so an unfinished last message behind it is not a turn in
// progress. Crush records no turn failure anywhere
// on disk, so an error cannot be told from a finished turn. Conversation
// content — message parts, file snapshots, todos, titles — is never decoded.
package crush

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, cross-compiles everywhere
)

// Processes lists running processes and tells when they started;
// process.System is the real one.
type Processes interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
	// StartTime returns when the process started, or false if it is not running.
	StartTime(ctx context.Context, pid int) (time.Time, bool)
}

// Source detects running Crush sessions.
type Source struct {
	Processes Processes
	// Store reads the per-project database of a data directory.
	Store Store
	// GlobalDir is Crush's global data directory, which holds projects.json.
	GlobalDir string
}

// NewSource returns a Source for the Crush installation of the user whose home
// directory is home: $CRUSH_GLOBAL_DATA when it is set, else $XDG_DATA_HOME,
// else ~/.local/share, for the global data directory that holds projects.json.
func NewSource(home string, processes Processes) *Source {
	return &Source{
		Processes: processes,
		Store:     SQLiteStore{},
		GlobalDir: DefaultGlobalDataDir(home),
	}
}

// DefaultGlobalDataDir is where Crush keeps its global data for the user whose
// home directory is home: $CRUSH_GLOBAL_DATA, else $XDG_DATA_HOME/crush, else
// ~/.local/share/crush.
func DefaultGlobalDataDir(home string) string {
	if dir := os.Getenv("CRUSH_GLOBAL_DATA"); dir != "" {
		return dir
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "crush")
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindCrush) }

// Sessions implements detect.Source. A folder whose database cannot be read is
// still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "crush")
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
		session, err := s.toSession(ctx, p, now)
		sessions = append(sessions, session)
		errs = append(errs, err)
	}
	return sessions, errors.Join(errs...)
}

// toSession builds the card of a folder: the newest session of its project
// data directory, or a waiting card when Crush has written none there yet.
func (s *Source) toSession(ctx context.Context, p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindCrush) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindCrush,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	dataDir := s.dataDir(p.Dir)
	if dataDir == "" {
		return session, nil // Crush has created no project data directory yet
	}
	sess, found, err := s.Store.Session(ctx, dataDir)
	if err != nil || !found {
		return session, err
	}
	session.ID = string(agent.KindCrush) + ":" + sess.ID
	if !sess.CreatedAt.IsZero() {
		session.StartedAt = sess.CreatedAt
	}
	if sess.Tokens > 0 {
		session.Context = &agent.ContextUsage{Used: sess.Tokens}
	}
	message, found, err := s.Store.LastMessage(ctx, dataDir, sess.ID)
	if err != nil || !found {
		return session, err // a session without messages has not been prompted yet
	}
	held := lockHeld(ctx, s.Processes, filepath.Join(dataDir, "crush.lock"), p.PID)
	session.Status, session.Since = statusOf(summaryOf(message), held, p.StartedAt, now)
	return session, nil
}

// dataDir returns the project data directory Crush keeps a working directory's
// state in: the one the global registry maps it to, else the nearest `.crush`
// directory at or above the working directory. Empty when there is none. A
// registry that cannot be read is not an error: the directories on disk are
// the fallback.
func (s *Source) dataDir(dir string) string {
	if dataDir := s.registered(dir); dataDir != "" {
		return dataDir
	}
	return nearestDataDir(dir)
}

// registered looks the working directory up in the global registry,
// projects.json, which maps the projects Crush has run to their data
// directories.
func (s *Source) registered(dir string) string {
	data, err := os.ReadFile(filepath.Join(s.GlobalDir, "projects.json"))
	if err != nil {
		return ""
	}
	var registry projectsFile
	if json.Unmarshal(data, &registry) != nil {
		return ""
	}
	for _, project := range registry.Projects {
		if project.Path == dir && project.DataDir != "" {
			return project.DataDir
		}
	}
	return ""
}

// projectsFile is the registry of the projects Crush has run in: their working
// directory mapped to their data directory. Only these fields are read;
// last_accessed is not.
type projectsFile struct {
	Projects []struct {
		Path    string `json:"path"`
		DataDir string `json:"data_dir"`
	} `json:"projects"`
}

// nearestDataDir walks up from dir to the root for the nearest `.crush`
// directory: Crush searches for its project data directory the same way,
// bounded by the git worktree root.
func nearestDataDir(dir string) string {
	for {
		candidate := filepath.Join(dir, ".crush")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// lock is the payload of the lock file Crush holds over a project's data
// directory. Only the pid is read.
type lock struct {
	PID int `json:"pid"`
}

// lockHeld reports whether a project's data directory is locked by the crush
// process the card belongs to: Crush writes the pid of its process to
// <data dir>/crush.lock and holds an OS-level lock over it for the process
// lifetime. A lock whose pid is not running is one a crashed run left behind,
// and one whose pid names another process does not hold this card either — a
// repository can commit a lock naming any live pid, so only the card's own
// process counts.
func lockHeld(ctx context.Context, pids Processes, path string, pid int) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var l lock
	if json.Unmarshal(data, &l) != nil || l.PID != pid {
		return false
	}
	_, running := pids.StartTime(ctx, l.PID)
	return running
}

// turnState is where the newest turn of a session is.
type turnState int

const (
	noTurn turnState = iota
	turnRunning
	turnEnded
)

// summary is what the newest message of a session says about its conversation.
type summary struct {
	turn turnState
	at   time.Time // when the newest turn started (running) or ended (ended)
}

// summaryOf maps the newest message of a session to the state of its turn: a
// user prompt is a turn whose reply has not arrived, an assistant message
// without finished_at one that is being written, an assistant message with
// finished_at (or anything else) a turn that is over.
func summaryOf(message Message) summary {
	switch {
	case message.Role == "user":
		at := message.PromptedAt
		if at.IsZero() {
			at = message.CreatedAt
		}
		return summary{turn: turnRunning, at: at}
	case message.Role == "assistant" && message.FinishedAt.IsZero():
		at := message.PromptedAt
		if at.IsZero() {
			at = message.CreatedAt
		}
		return summary{turn: turnRunning, at: at}
	default:
		at := message.FinishedAt
		if at.IsZero() {
			at = message.CreatedAt
		}
		return summary{turn: turnEnded, at: at}
	}
}

// statusOf maps the newest turn of a session to a status and when it began.
// started is when the crush process started: a message written before that
// belongs to a previous run, so it is neither still working nor this run's
// unfinished turn, and the session waits since it was opened. A turn only
// works while the project lock is held: an unfinished last message behind a
// lock whose pid is gone is a run that died mid-turn. Everything goes idle
// after 2 h.
func statusOf(summary summary, held bool, started, now time.Time) (agent.Status, time.Time) {
	live := !summary.at.Before(started)
	since := summary.at
	if !live {
		since = started
	}
	switch {
	case summary.turn == noTurn:
		return agent.StatusWaiting, since
	case live && summary.turn == turnRunning && held && now.Sub(since) < agent.DormantAfter:
		return agent.StatusWorking, since
	case now.Sub(since) >= agent.DormantAfter:
		return agent.StatusIdle, since
	default:
		return agent.StatusWaiting, since
	}
}

// Store reads the per-project databases of Crush.
type Store interface {
	// Session returns the project's active session: its newest one, without
	// the special sessions Crush runs for itself. found is false when there
	// is none.
	Session(ctx context.Context, dataDir string) (Session, bool, error)
	// LastMessage returns the newest message of a session, and when the
	// session's newest user prompt was sent. found is false when the session
	// has no messages.
	LastMessage(ctx context.Context, dataDir, sessionID string) (Message, bool, error)
}

// Session is the active session of a project, as its database records it.
type Session struct {
	ID        string
	CreatedAt time.Time // when the session was opened
	UpdatedAt time.Time
	Tokens    int // prompt and completion tokens of the session, 0 when Crush has recorded none
}

// Message is the newest message of a session. Only the role and the
// timestamps are read.
type Message struct {
	Role       string
	CreatedAt  time.Time
	FinishedAt time.Time // zero while the message is being written
	PromptedAt time.Time // when the session's newest user prompt was sent
}

// SQLiteStore reads a project's crush.db. Only session ids, timestamps, token
// counts and message roles are read: never a title, a message's parts, a file
// snapshot or any other conversation content.
type SQLiteStore struct{}

// The project's active session: its newest one, without the special sessions
// Crush runs for itself (title generation, agent tools).
const sessionQuery = `
SELECT id, created_at, updated_at, coalesce(prompt_tokens, 0) + coalesce(completion_tokens, 0)
FROM sessions
WHERE id NOT LIKE 'title-%' AND instr(id, '$$') = 0
ORDER BY updated_at DESC, created_at DESC
LIMIT 1`

// The newest message of a session, with the time of its newest user prompt:
// a turn starts there, so its age counts from the prompt.
const lastMessageQuery = `
SELECT m.role, m.created_at, m.finished_at,
       (SELECT max(u.created_at) FROM messages u WHERE u.session_id = m.session_id AND u.role = 'user')
FROM messages m
WHERE m.session_id = ?
ORDER BY m.created_at DESC, m.updated_at DESC
LIMIT 1`

// Session implements Store. A missing database, or no session in it, is no
// history, not an error.
func (s SQLiteStore) Session(ctx context.Context, dataDir string) (Session, bool, error) {
	var session Session
	var created, updated, tokens sql.NullInt64
	found, err := queryRow(ctx, filepath.Join(dataDir, "crush.db"), sessionQuery, nil,
		&session.ID, &created, &updated, &tokens)
	if !found || err != nil {
		return Session{}, false, err
	}
	session.CreatedAt = unixAt(created)
	session.UpdatedAt = unixAt(updated)
	session.Tokens = int(tokens.Int64)
	return session, true, nil
}

// LastMessage implements Store.
func (s SQLiteStore) LastMessage(ctx context.Context, dataDir, sessionID string) (Message, bool, error) {
	var message Message
	var role sql.NullString
	var created, finished, prompted sql.NullInt64
	found, err := queryRow(ctx, filepath.Join(dataDir, "crush.db"), lastMessageQuery, []any{sessionID},
		&role, &created, &finished, &prompted)
	if !found || err != nil {
		return Message{}, false, err
	}
	message.Role = role.String
	message.CreatedAt = unixAt(created)
	message.FinishedAt = unixAt(finished)
	message.PromptedAt = unixAt(prompted)
	return message, true, nil
}

// queryRow runs query with args against the database at path and scans its
// row. A missing database, or no row, is no history, not an error.
func queryRow(ctx context.Context, path, query string, args []any, dest ...any) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil // Crush has written no database here: no history, not an error
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return false, err
	}
	defer db.Close()

	err = db.QueryRowContext(ctx, query, args...).Scan(dest...)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

// dsn opens the database read-only. A lock held by Crush is waited for at most
// 250 ms, so a busy database cannot hold up the 1 s update loop for long.
func dsn(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // Windows: file:///C:/Users/...
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(250)")
	u.RawQuery = q.Encode()
	return u.String()
}

// unixAt converts a unix-seconds column to a time, zero when it is empty.
func unixAt(value sql.NullInt64) time.Time {
	if !value.Valid || value.Int64 == 0 {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0)
}
