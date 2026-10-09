package opencode

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// newDatabase creates a minimal opencode.db with the tables the store reads.
func newDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	schema := `
CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT NOT NULL, time_updated INTEGER NOT NULL);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
                      time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
                   time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteStoreReadsLatestMessageAndTool(t *testing.T) {
	path, db := newDatabase(t)
	exec(t, db, `INSERT INTO session VALUES ('s1', '/w/api', 2000)`)
	exec(t, db, `INSERT INTO message VALUES ('m1', 's1', 1000, 1000, '{"role":"user"}')`)
	exec(t, db, `INSERT INTO message VALUES ('m2', 's1', 1500, 1900,
		'{"role":"assistant","error":{"name":"APIError"},"time":{"completed":1900}}')`)
	exec(t, db, `INSERT INTO part VALUES ('p1', 'm2', 's1', 1600, 1700, '{"type":"tool","state":{"status":"running"}}')`)
	exec(t, db, `INSERT INTO part VALUES ('p2', 'm2', 's1', 1650, 1650, '{"type":"text"}')`)
	store := SQLiteStore{Path: path}
	ctx := context.Background()

	msg, found, err := store.LastMessage(ctx, "/w/api")
	if err != nil || !found {
		t.Fatalf("LastMessage: found=%v err=%v", found, err)
	}
	if msg.Role != "assistant" || msg.ErrorName != "APIError" || !msg.Completed || msg.UpdatedAt.UnixMilli() != 1900 {
		t.Errorf("message = %+v", msg)
	}

	part, found, err := store.LastToolPart(ctx, "/w/api")
	if err != nil || !found {
		t.Fatalf("LastToolPart: found=%v err=%v", found, err)
	}
	if part.State != "running" || part.UpdatedAt.UnixMilli() != 1700 {
		t.Errorf("part = %+v", part)
	}
}

func TestSQLiteStoreWithoutHistory(t *testing.T) {
	path, _ := newDatabase(t)
	ctx := context.Background()

	if _, found, err := (SQLiteStore{Path: path}).LastMessage(ctx, "/nowhere"); found || err != nil {
		t.Errorf("unknown dir: found=%v err=%v", found, err)
	}
	missing := SQLiteStore{Path: filepath.Join(t.TempDir(), "absent.db")}
	if _, found, err := missing.LastMessage(ctx, "/w"); found || err != nil {
		t.Errorf("missing database: found=%v err=%v", found, err)
	}
}

func TestDSNIsAnAbsoluteFileURI(t *testing.T) {
	dsn := SQLiteStore{Path: filepath.Join(t.TempDir(), "opencode.db")}.dsn()
	if !strings.HasPrefix(dsn, "file:///") || strings.Contains(dsn, `%5C`) {
		t.Errorf("dsn = %q, want file:///... with forward slashes", dsn)
	}
	if !strings.Contains(dsn, "mode=ro") {
		t.Errorf("dsn = %q, want read-only mode", dsn)
	}
}
