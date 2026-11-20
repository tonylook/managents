// Package goose detects Block Goose sessions.
//
// Every running `goose` process is a session for its working directory, and
// the folder's active conversation is the newest session Goose keeps for it
// in its sessions database — macOS: ~/Library/Application Support/Block/goose;
// elsewhere: $XDG_DATA_HOME/goose, default ~/.local/share; $GOOSE_PATH_ROOT
// moves the whole store to <root>/data — opened read-only, the session row
// named by its working_dir column and updated in place on every turn. Goose
// records no status anywhere: the role of the session's newest message
// decides, a prompt whose reply has not arrived being a turn in progress and
// an assistant message a finished one, and neither a turn failure nor a
// permission prompt is written to disk. Conversation content — message
// contents, recipes, session names and descriptions — is never decoded.
package goose

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, cross-compiles everywhere
)

// ProcessFinder lists running processes; process.System is the real one.
type ProcessFinder interface {
	// FindByName lists running processes whose executable name is exactly name.
	FindByName(ctx context.Context, name string) ([]process.Info, error)
}

// Source detects running Goose sessions.
type Source struct {
	Processes ProcessFinder
	// Store reads the sessions database.
	Store Store
}

// NewSource returns a Source for the Goose installation of the user whose
// home directory is home.
func NewSource(home string, processes ProcessFinder) *Source {
	return &Source{
		Processes: processes,
		Store:     SQLiteStore{Path: DefaultDatabasePath(home)},
	}
}

// DefaultDatabasePath is where Goose keeps its sessions database for the user
// whose home directory is home: on macOS ~/Library/Application Support/Block/
// goose/sessions.db, elsewhere $XDG_DATA_HOME/goose/sessions.db (default
// ~/.local/share), or <root>/data/sessions.db when $GOOSE_PATH_ROOT holds an
// absolute path.
func DefaultDatabasePath(home string) string {
	return sessionsPath(home, runtime.GOOS)
}

// sessionsPath is DefaultDatabasePath for the given system, so both layouts
// can be tested everywhere.
func sessionsPath(home, goos string) string {
	if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" && filepath.IsAbs(root) {
		return filepath.Join(root, "data", "sessions.db")
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Block", "goose", "sessions.db")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "goose", "sessions.db")
}

// Name implements detect.Source.
func (s *Source) Name() string { return string(agent.KindGoose) }

// Sessions implements detect.Source. A folder whose sessions cannot be read
// is still shown, as waiting, and the errors are returned.
func (s *Source) Sessions(ctx context.Context, now time.Time) ([]agent.Session, error) {
	procs, err := s.Processes.FindByName(ctx, "goose")
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

// toSession builds the card of a folder: the newest session Goose has recorded
// for it, or a waiting card when Goose has written none there yet.
func (s *Source) toSession(ctx context.Context, p process.Info, now time.Time) (agent.Session, error) {
	session := agent.Session{
		ID:        string(agent.KindGoose) + ":" + strconv.Itoa(p.PID),
		Kind:      agent.KindGoose,
		Dir:       p.Dir,
		Status:    agent.StatusWaiting,
		Since:     p.StartedAt,
		StartedAt: p.StartedAt,
	}
	sess, found, err := s.Store.Session(ctx, resolved(p.Dir))
	if err != nil || !found {
		return session, err // goose writes the session row with the first prompt
	}
	session.ID = string(agent.KindGoose) + ":" + sess.ID
	if !sess.CreatedAt.IsZero() {
		session.StartedAt = sess.CreatedAt
	}
	if sess.Tokens > 0 {
		session.Context = &agent.ContextUsage{Used: sess.Tokens}
	}
	message, found, err := s.Store.LastMessage(ctx, sess.ID)
	if err != nil || !found {
		return session, err // a session without messages has not been prompted yet
	}
	session.Status, session.Since = statusOf(summaryOf(message), p.StartedAt, now)
	return session, nil
}

// resolved is the working directory with symlinks resolved: Goose records the
// process's real working directory.
func resolved(dir string) string {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
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
// user prompt is a turn whose reply has not arrived, an assistant message a
// turn that is over. Goose records no other turn state.
func summaryOf(message Message) summary {
	if message.Role == "user" {
		return summary{turn: turnRunning, at: message.CreatedAt}
	}
	return summary{turn: turnEnded, at: message.CreatedAt}
}

// statusOf maps the newest turn of a session to a status and when it began.
// started is when the goose process started: a message written before that
// belongs to a previous run, so it is neither still working nor this run's
// unfinished turn, and the session waits since it was opened. A prompt whose
// reply has not arrived works only while it is young: one this old is a run
// that died mid-turn. Everything goes idle after 2 h.
func statusOf(summary summary, started, now time.Time) (agent.Status, time.Time) {
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
	default:
		return agent.StatusWaiting, since
	}
}

// Store reads Goose's sessions database.
type Store interface {
	// Session returns a folder's active session: its newest one that is not
	// archived. found is false when there is none.
	Session(ctx context.Context, dir string) (Session, bool, error)
	// LastMessage returns the role and the time of a session's newest
	// message. found is false when the session has no messages.
	LastMessage(ctx context.Context, sessionID string) (Message, bool, error)
}

// Session is a folder's active session, as the database records it.
type Session struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Tokens    int // the session's token count, 0 when Goose has recorded none
}

// Message is the newest message of a session. Only the role and the time are
// read.
type Message struct {
	Role      string
	CreatedAt time.Time
}

// SQLiteStore reads Goose's sessions database. Only session ids, working
// directories, timestamps and token counts, and message roles and times are
// read: never a message's content, a recipe, a session name or any other
// conversation content.
type SQLiteStore struct {
	Path string
}

// A folder's sessions: not archived, newest last. The rows are dated here
// rather than by the database, because Goose writes RFC 3339 for the sessions
// it inserts and SQLite's own datetime() for the columns it updates in place,
// and the two do not sort alike.
const sessionsQuery = `
SELECT id, created_at, updated_at, coalesce(total_tokens, 0)
FROM sessions WHERE working_dir = ? AND archived_at IS NULL`

// The newest message of a session, the way Goose orders them itself.
const lastMessageQuery = `
SELECT role, created_timestamp FROM messages
WHERE session_id = ?
ORDER BY created_timestamp DESC, id DESC
LIMIT 1`

// Session implements Store. A missing database, or no session in the folder,
// is no history, not an error.
func (s SQLiteStore) Session(ctx context.Context, dir string) (Session, bool, error) {
	if _, err := os.Stat(s.Path); errors.Is(err, fs.ErrNotExist) {
		return Session{}, false, nil // Goose never ran here: no history, not an error
	}
	db, err := sql.Open("sqlite", dsn(s.Path))
	if err != nil {
		return Session{}, false, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, sessionsQuery, dir)
	if err != nil {
		return Session{}, false, err
	}
	defer rows.Close()

	var newest Session
	for rows.Next() {
		var session Session
		var created, updated string
		var tokens sql.NullInt64
		if err := rows.Scan(&session.ID, &created, &updated, &tokens); err != nil {
			return Session{}, false, err
		}
		session.CreatedAt, session.UpdatedAt = parseTime(created), parseTime(updated)
		session.Tokens = int(tokens.Int64)
		if newest.ID == "" || session.UpdatedAt.After(newest.UpdatedAt) {
			newest = session
		}
	}
	return newest, newest.ID != "", rows.Err()
}

// LastMessage implements Store.
func (s SQLiteStore) LastMessage(ctx context.Context, sessionID string) (Message, bool, error) {
	if _, err := os.Stat(s.Path); errors.Is(err, fs.ErrNotExist) {
		return Message{}, false, nil // Goose never ran here: no history, not an error
	}
	db, err := sql.Open("sqlite", dsn(s.Path))
	if err != nil {
		return Message{}, false, err
	}
	defer db.Close()

	var message Message
	var role string
	var created int64
	err = db.QueryRowContext(ctx, lastMessageQuery, sessionID).Scan(&role, &created)
	switch {
	case err == nil:
		message.Role, message.CreatedAt = role, unixAt(created)
		return message, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return Message{}, false, nil
	default:
		return Message{}, false, err
	}
}

// dsn opens the database read-only. A lock held by Goose is waited for at most
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

// millisecondThreshold is Goose's own: timestamps above it are unix
// milliseconds, the rest unix seconds — message times were written as either,
// whichever the writer used.
const millisecondThreshold = 10_000_000_000

// sqliteDatetime is the layout of SQLite's datetime(), which Goose's in-place
// updates write: the same instant as RFC 3339, without the T and the zone.
const sqliteDatetime = "2006-01-02 15:04:05"

// parseTime reads a timestamp column: RFC 3339, SQLite's datetime(), or a
// bare unix timestamp.
func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	if t, err := time.ParseInLocation(sqliteDatetime, value, time.UTC); err == nil {
		return t
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		return unixAt(n)
	}
	return time.Time{}
}

// unixAt converts a unix timestamp in seconds or milliseconds to a time, zero
// when it is empty.
func unixAt(value int64) time.Time {
	switch {
	case value == 0:
		return time.Time{}
	case value >= millisecondThreshold:
		return time.UnixMilli(value)
	default:
		return time.Unix(value, 0)
	}
}
