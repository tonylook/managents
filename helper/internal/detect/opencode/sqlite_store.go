package opencode

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, cross-compiles everywhere
)

// SQLiteStore reads OpenCode's opencode.db. All JSON fields are extracted by
// SQLite itself, so message content never reaches this process.
type SQLiteStore struct {
	Path string
}

// DefaultDatabasePath is where OpenCode keeps its database.
func DefaultDatabasePath(home string) string {
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

const lastMessageQuery = `
SELECT json_extract(m.data, '$.role'),
       coalesce(json_extract(m.data, '$.error.name'), ''),
       json_extract(m.data, '$.time.completed') IS NOT NULL,
       m.time_updated
FROM message m JOIN session s ON s.id = m.session_id
WHERE s.directory = ?
ORDER BY m.time_created DESC
LIMIT 1`

const lastToolPartQuery = `
SELECT json_extract(p.data, '$.state.status'), p.time_updated
FROM part p
WHERE p.session_id = (SELECT id FROM session WHERE directory = ? ORDER BY time_updated DESC LIMIT 1)
  AND json_extract(p.data, '$.type') = 'tool'
ORDER BY p.time_created DESC
LIMIT 1`

// LastMessage implements Store.
func (s SQLiteStore) LastMessage(ctx context.Context, dir string) (Message, bool, error) {
	var msg Message
	var role sql.NullString
	var updated int64
	found, err := s.queryRow(ctx, lastMessageQuery, dir, &role, &msg.ErrorName, &msg.Completed, &updated)
	if !found || err != nil {
		return Message{}, false, err
	}
	msg.Role = role.String
	msg.UpdatedAt = time.UnixMilli(updated)
	return msg, true, nil
}

// LastToolPart implements Store.
func (s SQLiteStore) LastToolPart(ctx context.Context, dir string) (ToolPart, bool, error) {
	var part ToolPart
	var state sql.NullString
	var updated int64
	found, err := s.queryRow(ctx, lastToolPartQuery, dir, &state, &updated)
	if !found || err != nil {
		return ToolPart{}, false, err
	}
	part.State = state.String
	part.UpdatedAt = time.UnixMilli(updated)
	return part, true, nil
}

func (s SQLiteStore) queryRow(ctx context.Context, query, dir string, dest ...any) (bool, error) {
	if _, err := os.Stat(s.Path); errors.Is(err, os.ErrNotExist) {
		return false, nil // OpenCode never ran here: no history, not an error
	}
	db, err := sql.Open("sqlite", s.dsn())
	if err != nil {
		return false, err
	}
	defer db.Close()

	err = db.QueryRowContext(ctx, query, dir).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// dsn opens the database read-only and waits briefly if OpenCode holds a lock.
func (s SQLiteStore) dsn() string {
	u := url.URL{Scheme: "file", Path: s.Path}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(2000)")
	u.RawQuery = q.Encode()
	return u.String()
}
