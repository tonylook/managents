package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/protocol"
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
CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT NOT NULL, title TEXT NOT NULL DEFAULT '',
                      time_updated INTEGER NOT NULL);
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
	exec(t, db, `INSERT INTO session (id, directory, time_updated) VALUES ('s1', '/w/api', 2000)`)
	exec(t, db, `INSERT INTO message VALUES ('m1', 's1', 1000, 1000, '{"role":"user"}')`)
	exec(t, db, `INSERT INTO message VALUES ('m2', 's1', 1500, 1900,
		'{"role":"assistant","parentID":"m1","error":{"name":"APIError"},"time":{"completed":1900}}')`)
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
	if msg.TurnStartedAt.UnixMilli() != 1000 {
		t.Errorf("turn started at %d, want 1000: when its prompt m1 was sent", msg.TurnStartedAt.UnixMilli())
	}

	part, found, err := store.LastToolPart(ctx, "/w/api")
	if err != nil || !found {
		t.Fatalf("LastToolPart: found=%v err=%v", found, err)
	}
	if part.State != "running" || part.UpdatedAt.UnixMilli() != 1700 {
		t.Errorf("part = %+v", part)
	}
}

func TestSQLiteStoreFollowsTheNewestSessionOfAFolder(t *testing.T) {
	path, db := newDatabase(t)
	// A subagent runs in a child session of the same folder. The parent's task
	// tool stays "running", without updates, until the subagent is done.
	exec(t, db, `INSERT INTO session (id, directory, time_updated) VALUES ('parent', '/w/api', 2000)`)
	exec(t, db, `INSERT INTO session (id, parent_id, directory, time_updated) VALUES ('child', 'parent', '/w/api', 2500)`)
	exec(t, db, `INSERT INTO message VALUES ('m1', 'parent', 1000, 1000, '{"role":"user"}')`)
	exec(t, db, `INSERT INTO message VALUES ('m2', 'parent', 1100, 1600, '{"role":"assistant","parentID":"m1"}')`)
	exec(t, db, `INSERT INTO part VALUES ('p1', 'm2', 'parent', 1200, 1600, '{"type":"tool","state":{"status":"running"}}')`)
	exec(t, db, `INSERT INTO message VALUES ('c1', 'child', 1700, 1700, '{"role":"user"}')`)
	exec(t, db, `INSERT INTO message VALUES ('c2', 'child', 1800, 2400, '{"role":"assistant","parentID":"c1"}')`)
	exec(t, db, `INSERT INTO part VALUES ('p2', 'c2', 'child', 2300, 2400, '{"type":"tool","state":{"status":"completed"}}')`)
	store := SQLiteStore{Path: path}
	ctx := context.Background()

	msg, _, err := store.LastMessage(ctx, "/w/api")
	if err != nil || msg.UpdatedAt.UnixMilli() != 2400 || msg.TurnStartedAt.UnixMilli() != 1700 {
		t.Errorf("LastMessage = %+v, %v; want the subagent's reply", msg, err)
	}
	part, _, err := store.LastToolPart(ctx, "/w/api")
	if err != nil || part.State != "completed" {
		t.Errorf("LastToolPart = %+v, %v; want the subagent's tool, not the parent's task tool", part, err)
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-7f3a"
	path, db := newDatabase(t)
	at := now.UnixMilli()
	exec(t, db, `INSERT INTO session (id, directory, title, time_updated) VALUES ('s1', '/w/api', ?, ?)`, canary, at)
	exec(t, db, `INSERT INTO message VALUES ('m1', 's1', ?, ?, json_object('role', 'user', 'summary', json_object('title', ?)))`,
		at-2000, at-2000, canary)
	exec(t, db, `INSERT INTO part VALUES ('p1', 'm1', 's1', ?, ?, json_object('type', 'text', 'text', ?))`, at-2000, at-2000, canary)
	exec(t, db, `INSERT INTO message VALUES ('m2', 's1', ?, ?, json_object('role', 'assistant', 'parentID', 'm1'))`, at-1000, at)
	exec(t, db, `INSERT INTO part VALUES ('p2', 'm2', 's1', ?, ?, json_object('type', 'tool', 'tool', 'bash',
		'state', json_object('status', 'running', 'input', json_object('command', ?), 'output', ?)))`, at-500, at, canary, canary)
	source := &Source{
		Processes: fakeProcesses{{PID: 7, Dir: "/w/api", StartedAt: now.Add(-time.Hour)}},
		Store:     SQLiteStore{Path: path},
	}

	sessions, err := source.Sessions(context.Background(), now)

	if err != nil || len(sessions) != 1 || sessions[0].Status != agent.StatusWorking {
		t.Fatalf("sessions = %+v, %v; want the session read from its history, working", sessions, err)
	}
	detected, err := json.Marshal(sessions)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := protocol.Encode(protocol.NewState(agent.Arrange(sessions), now))
	if err != nil {
		t.Fatal(err)
	}
	for what, data := range map[string][]byte{"sessions": detected, "frame": frame} {
		if bytes.Contains(data, []byte(canary)) {
			t.Errorf("conversation content leaked into the %s: %s", what, data)
		}
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

func TestDefaultDatabasePath(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(t.TempDir(), "data")
	tests := []struct {
		name, env, want string
	}{
		{"default", "", filepath.Join(home, ".local", "share", "opencode", "opencode.db")},
		{"XDG_DATA_HOME", data, filepath.Join(data, "opencode", "opencode.db")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", tt.env)
			if got := DefaultDatabasePath(home); got != tt.want {
				t.Errorf("DefaultDatabasePath = %q, want %q", got, tt.want)
			}
		})
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
