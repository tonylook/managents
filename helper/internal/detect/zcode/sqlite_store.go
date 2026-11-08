package zcode

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, cross-compiles everywhere
)

// SQLiteStore reads ZCode's database, db.sqlite in its data folder. Only ids,
// statuses and timestamps are read: never a title, a message, a prompt or any
// other conversation content.
type SQLiteStore struct {
	Path string
}

// DefaultDatabasePath is where ZCode keeps its database for the user whose
// home directory is home: ~/.zcode/cli/db/db.sqlite.
func DefaultDatabasePath(home string) string {
	return filepath.Join(home, ".zcode", "cli", "db", "db.sqlite")
}

// The open conversation of a folder: the session ZCode updated last there
// that is neither deleted nor archived.
const lastSessionQuery = `
SELECT id FROM session
WHERE directory = ? AND time_deleted IS NULL AND time_archived IS NULL
ORDER BY time_updated DESC, time_created DESC
LIMIT 1`

// The newest turn of a conversation. ZCode writes the row when the turn
// ends; cancelled_by_user tells a user's interruption from a failure.
const lastTurnQuery = `
SELECT status, started_at, completed_at, cancelled_by_user
FROM turn_usage WHERE session_id = ?
ORDER BY started_at DESC LIMIT 1`

// The newest prompt admitted to the conversation, written the moment it is
// sent. One that was queued is admitted later, as promoted.
const lastInputQuery = `
SELECT time_created FROM session_input
WHERE session_id = ? AND status = 'promoted'
ORDER BY time_created DESC LIMIT 1`

// LastSession implements Store. A missing database, or no session in the
// folder, is no history, not an error.
func (s SQLiteStore) LastSession(ctx context.Context, dir string) (Session, bool, error) {
	var session Session
	found, err := s.queryRow(ctx, lastSessionQuery, dir, &session.ID)
	if !found || err != nil {
		return Session{}, false, err
	}
	return session, true, nil
}

// LastTurn implements Store.
func (s SQLiteStore) LastTurn(ctx context.Context, sessionID string) (Turn, bool, error) {
	var turn Turn
	var started int64
	var completed sql.NullInt64
	found, err := s.queryRow(ctx, lastTurnQuery, sessionID,
		&turn.Status, &started, &completed, &turn.CancelledByUser)
	if !found || err != nil {
		return Turn{}, false, err
	}
	turn.StartedAt = time.UnixMilli(started)
	if completed.Valid {
		turn.CompletedAt = time.UnixMilli(completed.Int64)
	}
	return turn, true, nil
}

// LastInput implements Store.
func (s SQLiteStore) LastInput(ctx context.Context, sessionID string) (Input, bool, error) {
	var input Input
	var sent int64
	found, err := s.queryRow(ctx, lastInputQuery, sessionID, &sent)
	if !found || err != nil {
		return Input{}, false, err
	}
	input.SentAt = time.UnixMilli(sent)
	return input, true, nil
}

// queryRow runs query with arg and scans its row. A missing database, or no
// row, is no history, not an error.
func (s SQLiteStore) queryRow(ctx context.Context, query, arg string, dest ...any) (bool, error) {
	if _, err := os.Stat(s.Path); errors.Is(err, fs.ErrNotExist) {
		return false, nil // ZCode never ran here: no history, not an error
	}
	db, err := sql.Open("sqlite", dsn(s.Path))
	if err != nil {
		return false, err
	}
	defer db.Close()

	err = db.QueryRowContext(ctx, query, arg).Scan(dest...)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, err
	}
}

// dsn opens the database read-only. A lock held by ZCode is waited for at
// most 250 ms, so a busy database cannot hold up the 1 s update loop for long.
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
