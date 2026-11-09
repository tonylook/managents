package zcode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/protocol"
)

// newDatabase creates ZCode's tables in a temporary database and opens it for
// writing, the way ZCode itself would.
func newDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(250)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	exec(t, db, `CREATE TABLE session (
        id text primary key, directory text not null, title text not null,
        time_created integer not null, time_updated integer not null,
        time_archived integer, time_deleted integer, last_outcome text)`)
	exec(t, db, `CREATE TABLE session_input (
        id text primary key, session_id text not null, kind text not null,
        delivery text not null, payload text not null, status text not null,
        time_created integer not null, time_updated integer not null)`)
	exec(t, db, `CREATE TABLE turn_usage (
        session_id text not null, turn_id text not null, status text not null,
        started_at integer not null, completed_at integer, cancelled_by_user integer not null default 0,
        error_type text, primary key(session_id, turn_id))`)
	exec(t, db, `CREATE TABLE message (
        id text primary key, session_id text not null, time_created integer not null,
        time_updated integer not null, data text not null)`)
	exec(t, db, `CREATE TABLE part (
        id text primary key, session_id text not null, time_created integer not null,
        time_updated integer not null, data text not null)`)
	exec(t, db, `CREATE TABLE todo (session_id text not null, content text not null)`)
	exec(t, db, `CREATE TABLE input_history (id text primary key, text text not null)`)
	exec(t, db, `CREATE TABLE session_entry (id text primary key, session_id text not null,
        type text not null, data text not null)`)
	exec(t, db, `CREATE TABLE tool_usage (id text primary key, error_message text)`)
	exec(t, db, `CREATE TABLE model_usage (id text primary key, error_message text, raw_usage_json text)`)
	return path, db
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestSQLiteStore(t *testing.T) {
	path, db := newDatabase(t)
	at := now.UnixMilli()
	exec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated)
        VALUES ('s-old', '/w/api', '', ?-90000, ?-80000)`, at, at)
	exec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated)
        VALUES ('s-new', '/w/api', '', ?-60000, ?-10000)`, at, at)
	exec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated, time_archived)
        VALUES ('s-archived', '/w/api', '', ?-95000, ?-5000, ?-4000)`, at, at, at)
	exec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated, time_deleted)
        VALUES ('s-deleted', '/w/api', '', ?-95000, ?-5000, ?-4000)`, at, at, at)
	exec(t, db, `INSERT INTO turn_usage VALUES ('s-new', 't1', 'completed', ?-50000, ?-40000, 0, NULL)`, at, at)
	exec(t, db, `INSERT INTO turn_usage VALUES ('s-new', 't2', 'error', ?-30000, ?-20000, 1, 'stopped')`, at, at)
	exec(t, db, `INSERT INTO session_input VALUES ('i1', 's-new', 'sendText', 'startNow', '{}', 'promoted', ?-50000, ?-50000)`, at, at)
	exec(t, db, `INSERT INTO session_input VALUES ('i2', 's-new', 'sendText', 'queue', '{}', 'admitted', ?-25000, ?-25000)`, at, at)

	store := SQLiteStore{Path: path}
	ctx := context.Background()

	sessions, err := store.Sessions(ctx, "/w/api")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	if want := []string{"s-new", "s-old"}; !slices.Equal(ids, want) {
		t.Errorf("Sessions = %s, want %s: newest write first, archived and deleted left out", ids, want)
	}
	if len(sessions) > 0 && !sessions[0].UpdatedAt.Equal(time.UnixMilli(at-10000)) {
		t.Errorf("s-new UpdatedAt = %s, want %s", sessions[0].UpdatedAt, time.UnixMilli(at-10000))
	}
	if sessions, err := store.Sessions(ctx, "/w/none"); len(sessions) != 0 || err != nil {
		t.Errorf("Sessions of a folder without sessions = %v, %v; want no history", sessions, err)
	}

	turn, found, err := store.LastTurn(ctx, "s-new")
	if err != nil || !found {
		t.Fatalf("LastTurn = %+v, %v, %v", turn, found, err)
	}
	wantTurn := Turn{
		Status: "error", CancelledByUser: true,
		StartedAt: time.UnixMilli(at - 30000), CompletedAt: time.UnixMilli(at - 20000),
	}
	if turn != wantTurn {
		t.Errorf("LastTurn = %+v, want %+v", turn, wantTurn)
	}
	if _, found, err := store.LastTurn(ctx, "s-old"); found || err != nil {
		t.Errorf("LastTurn of a session without turns = %v, %v; want none", found, err)
	}

	input, found, err := store.LastInput(ctx, "s-new")
	if err != nil || !found || !input.SentAt.Equal(time.UnixMilli(at-50000)) {
		t.Errorf("LastInput = %+v, %v, %v; want the promoted prompt", input, found, err)
	}
}

func TestSQLiteStoreRunningTurn(t *testing.T) {
	path, db := newDatabase(t)
	at := now.UnixMilli()
	exec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated)
        VALUES ('s1', '/w/api', '', ?-60000, ?-10000)`, at, at)
	// A turn row written live: no completion yet.
	exec(t, db, `INSERT INTO turn_usage VALUES ('s1', 't1', 'running', ?-10000, NULL, 0, NULL)`, at)

	turn, found, err := SQLiteStore{Path: path}.LastTurn(context.Background(), "s1")
	if err != nil || !found {
		t.Fatalf("LastTurn = %+v, %v, %v", turn, found, err)
	}
	if turn.Status != "running" || !turn.CompletedAt.IsZero() {
		t.Errorf("LastTurn = %+v, want a running turn without its end", turn)
	}
}

func TestSQLiteStoreWithoutDatabase(t *testing.T) {
	store := SQLiteStore{Path: filepath.Join(t.TempDir(), "db.sqlite")}
	ctx := context.Background()
	if sessions, err := store.Sessions(ctx, "/w/api"); sessions != nil || err != nil {
		t.Errorf("Sessions = %v, %v; want no history, not an error", sessions, err)
	}
	if _, found, err := store.LastTurn(ctx, "s1"); found || err != nil {
		t.Errorf("LastTurn = %v, %v; want no history, not an error", found, err)
	}
	if _, found, err := store.LastInput(ctx, "s1"); found || err != nil {
		t.Errorf("LastInput = %v, %v; want no history, not an error", found, err)
	}
}

func TestSQLiteStoreForeignSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec(t, db, `CREATE TABLE session (id text primary key)`) // a newer ZCode renamed the columns

	_, err = SQLiteStore{Path: path}.Sessions(context.Background(), "/w/api")
	if err == nil {
		t.Error("Sessions on a foreign schema = no error, want one")
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-7f3a"
	path, db := newDatabase(t)
	at := now.UnixMilli()
	exec(t, db, `INSERT INTO session (id, directory, title, time_created, time_updated)
        VALUES ('s1', '/w/api', ?, ?-60000, ?-10000)`, canary, at, at)
	// The turn before the prompt has ended; the prompt's turn is still running.
	exec(t, db, `INSERT INTO turn_usage VALUES ('s1', 't1', 'completed', ?-50000, ?-40000, 0, ?)`, at, at, canary)
	exec(t, db, `INSERT INTO session_input VALUES ('i1', 's1', 'sendText', 'startNow', ?, 'promoted', ?-10000, ?-10000)`,
		canary, at, at)
	exec(t, db, `INSERT INTO session_input VALUES ('i2', 's1', 'sendText', 'startNow', ?, 'discarded', ?-20000, ?-20000)`,
		canary, at, at)
	exec(t, db, `INSERT INTO message VALUES ('m1', 's1', ?-10000, ?-10000, json_object('role', 'user', 'text', ?))`, at, at, canary)
	exec(t, db, `INSERT INTO part VALUES ('p1', 's1', ?-10000, ?-10000, json_object('type', 'text', 'text', ?))`, at, at, canary)
	exec(t, db, `INSERT INTO todo VALUES ('s1', ?)`, canary)
	exec(t, db, `INSERT INTO input_history VALUES ('h1', ?)`, canary)
	exec(t, db, `INSERT INTO session_entry VALUES ('e1', 's1', 'runtime/execution_state', ?)`, canary)
	exec(t, db, `INSERT INTO tool_usage VALUES ('tu1', ?)`, canary)
	exec(t, db, `INSERT INTO model_usage VALUES ('mu1', ?, ?)`, canary, canary)
	source := &Source{
		Processes: fakeProcesses{cli(7, "/w/api", now.Add(-time.Hour))},
		Store:     SQLiteStore{Path: path},
		dataDir:   dataDir,
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWorking {
		t.Fatalf("sessions = %+v, want the session read from its history, working", sessions)
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
