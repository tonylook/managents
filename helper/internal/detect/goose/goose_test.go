package goose

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
	"github.com/tonylook/managents/helper/internal/protocol"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// fakeProcesses stands in for the process list.
type fakeProcesses []process.Info

func (f *fakeProcesses) FindByName(_ context.Context, name string) ([]process.Info, error) {
	if name != "goose" {
		return nil, nil
	}
	return *f, nil
}

// fakeStore stands in for the sessions database.
type fakeStore struct {
	// dir is the working directory that has history.
	dir      string
	session  Session
	found    bool
	message  Message
	msgFound bool
	err      error
	// asked records the working directory the store was last asked about.
	asked string
}

func (f *fakeStore) Session(_ context.Context, dir string) (Session, bool, error) {
	f.asked = dir
	if f.err != nil {
		return Session{}, false, f.err
	}
	if dir != f.dir {
		return Session{}, false, nil // another folder, no history
	}
	return f.session, f.found, nil
}

func (f *fakeStore) LastMessage(_ context.Context, _ string) (Message, bool, error) {
	if f.err != nil {
		return Message{}, false, f.err
	}
	return f.message, f.msgFound, nil
}

type fixture struct {
	t      *testing.T
	home   string
	procs  *fakeProcesses
	store  *fakeStore
	source *Source
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("GOOSE_PATH_ROOT", "") // the developer's own setting must not leak in
	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	procs := &fakeProcesses{}
	store := &fakeStore{}
	source := NewSource(home, procs)
	source.Store = store // the fake, not the developer's own database
	return &fixture{t: t, home: home, procs: procs, store: store, source: source}
}

// addProcess registers a running goose process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	*f.procs = append(*f.procs, process.Info{PID: pid, Name: "goose", Dir: dir, StartedAt: started})
}

// setSession fills the store's answer for dir: the folder's active session
// and its newest message.
func (f *fixture) setSession(dir string, session Session, message Message) {
	f.store.dir = dir
	f.store.session, f.store.found = session, true
	f.store.message, f.store.msgFound = message, true
}

func (f *fixture) sessions() []agent.Session {
	f.t.Helper()
	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		f.t.Fatal(err)
	}
	return sessions
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		started time.Time
		message Message
		want    agent.Status
		wantAge time.Duration
	}{
		{
			name:    "a prompt without its reply is working",
			started: now.Add(-time.Hour),
			message: Message{Role: "user", CreatedAt: now.Add(-30 * time.Second)},
			want:    agent.StatusWorking,
			wantAge: 30 * time.Second,
		},
		{
			name:    "an assistant message is a finished turn, waiting",
			started: now.Add(-time.Hour),
			message: Message{Role: "assistant", CreatedAt: now.Add(-20 * time.Second)},
			want:    agent.StatusWaiting,
			wantAge: 20 * time.Second,
		},
		{
			name:    "a system message is a finished turn too",
			started: now.Add(-time.Hour),
			message: Message{Role: "system", CreatedAt: now.Add(-time.Minute)},
			want:    agent.StatusWaiting,
		},
		{
			name:    "a prompt from a previous run is not working",
			started: now.Add(-time.Hour),
			message: Message{Role: "user", CreatedAt: now.Add(-3 * time.Hour)},
			want:    agent.StatusWaiting,
		},
		{
			name:    "a prompt without its turn that hung for hours is idle",
			started: now.Add(-4 * time.Hour),
			message: Message{Role: "user", CreatedAt: now.Add(-3 * time.Hour)},
			want:    agent.StatusIdle,
		},
		{
			name:    "a finished turn untouched for hours is idle",
			started: now.Add(-4 * time.Hour),
			message: Message{Role: "assistant", CreatedAt: now.Add(-3 * time.Hour)},
			want:    agent.StatusIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := f.project("payments-api")
			f.addProcess(100, dir, tt.started)
			f.setSession(dir, Session{ID: "session-a", CreatedAt: tt.started}, tt.message)

			sessions := f.sessions()

			if len(sessions) != 1 {
				t.Fatalf("got %d sessions, want 1", len(sessions))
			}
			if sessions[0].Status != tt.want {
				t.Errorf("status = %s, want %s", sessions[0].Status, tt.want)
			}
			if tt.wantAge != 0 && sessions[0].Age(now) != tt.wantAge {
				t.Errorf("age = %v, want %v", sessions[0].Age(now), tt.wantAge)
			}
		})
	}
}

func TestSessionFields(t *testing.T) {
	f := newFixture(t)
	dir := f.project("agent-lights")
	started := now.Add(-time.Hour)
	f.addProcess(4242, dir, started)
	opened := now.Add(-50 * time.Minute)
	f.setSession(dir, Session{ID: "session-abc", CreatedAt: opened, Tokens: 9_000},
		Message{Role: "assistant", CreatedAt: now.Add(-42 * time.Second)})

	s := f.sessions()[0]

	if s.ID != "goose:session-abc" || s.Kind != agent.KindGoose || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(opened) {
		t.Errorf("startedAt = %v, want the session's start %v", s.StartedAt, opened)
	}
	if s.Context == nil || s.Context.Used != 9_000 || s.Context.Limit != 0 {
		t.Errorf("context = %+v, want 9000 used, no limit (goose stores no window size)", s.Context)
	}
	if got := s.Age(now); got != 42*time.Second {
		t.Errorf("age = %v, want 42s", got)
	}
}

func TestNoSessionYetIsWaitingCard(t *testing.T) {
	f := newFixture(t)
	started := now.Add(-time.Hour)
	f.addProcess(7, f.project("website"), started)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "goose:7" || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card for the process", sessions)
	}
	if !sessions[0].Since.Equal(started) {
		t.Errorf("since = %v, want the process start %v", sessions[0].Since, started)
	}
}

func TestSkipsExitedProcessesAndOtherFolders(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addProcess(1, dir, now.Add(-2*time.Hour))
	f.addProcess(2, dir, now.Add(-time.Hour)) // two processes, one card
	f.setSession(dir, Session{ID: "session-a", CreatedAt: now.Add(-time.Hour)},
		Message{Role: "assistant", CreatedAt: now.Add(-time.Minute)})
	f.addProcess(3, "", now.Add(-time.Hour)) // no working directory: not a session
	f.addProcess(4, f.project("exited"), now.Add(-time.Hour))

	sessions := f.sessions()

	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want the payments-api card and a waiting card for exited", sessions)
	}
	if sessions[0].ID != "goose:session-a" || sessions[0].Dir != dir {
		t.Errorf("first card = %+v, want payments-api's session", sessions[0])
	}
	if sessions[1].ID != "goose:4" || sessions[1].Status != agent.StatusWaiting {
		t.Errorf("second card = %+v, want a waiting card for pid 4", sessions[1])
	}
}

func TestMatchesTheRealWorkingDirectory(t *testing.T) {
	f := newFixture(t)
	real := f.project("real-dir")
	link := filepath.Join(f.home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f.addProcess(8, link, now.Add(-time.Hour))
	f.setSession(real, Session{ID: "session-a", CreatedAt: now.Add(-time.Hour)},
		Message{Role: "assistant", CreatedAt: now.Add(-time.Minute)})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "goose:session-a" {
		t.Fatalf("sessions = %+v, want the session Goose recorded under the real directory", sessions)
	}
	if f.store.asked != real {
		t.Errorf("store asked for %q, want the real directory %q", f.store.asked, real)
	}
}

func TestStoreErrorIsReturnedButCardShown(t *testing.T) {
	f := newFixture(t)
	f.addProcess(9, f.project("payments-api"), now.Add(-time.Hour))
	f.store.err = fmt.Errorf("schema changed")

	sessions, err := f.source.Sessions(context.Background(), now)
	if err == nil {
		t.Fatal("err = nil, want the store error")
	}
	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card despite the error", sessions)
	}
}

// project makes a working directory under the temporary home, the real path
// with symlinks resolved, the way Goose records it.
func (f *fixture) project(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "work", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir
}

func TestDatabasePath(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "goose-root")
	xdg := filepath.Join(t.TempDir(), "xdg")
	relative := "goose-root-relative"
	tests := []struct {
		name   string
		goos   string
		env    string
		xdgEnv string
		want   string
	}{
		{"macOS default", "darwin", "", "", filepath.Join(home, "Library", "Application Support", "Block", "goose", "sessions.db")},
		{"Linux default", "linux", "", "", filepath.Join(home, ".local", "share", "goose", "sessions.db")},
		{"XDG_DATA_HOME", "linux", "", xdg, filepath.Join(xdg, "goose", "sessions.db")},
		{"GOOSE_PATH_ROOT", "darwin", root, "", filepath.Join(root, "data", "sessions.db")},
		{"a relative GOOSE_PATH_ROOT is ignored", "darwin", relative, xdg,
			filepath.Join(home, "Library", "Application Support", "Block", "goose", "sessions.db")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GOOSE_PATH_ROOT", tt.env)
			t.Setenv("XDG_DATA_HOME", tt.xdgEnv)
			if got := sessionsPath(home, tt.goos); got != tt.want {
				t.Errorf("database path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLiteStore(t *testing.T) {
	path, db := newDatabase(t)
	insertSession(t, db, "s-old", "/w/api", stamp(now.Add(-3*time.Hour)), stamp(now.Add(-2*time.Hour)), 100)
	insertSession(t, db, "s-archived", "/w/api", stamp(now.Add(-4*time.Hour)), stamp(now.Add(-time.Minute)), 200)
	insertSession(t, db, "s-new", "/w/api", stamp(now.Add(-time.Hour)), sqliteStamp(now.Add(-time.Second)), 7000)
	insertSession(t, db, "s-other", "/w/website", stamp(now.Add(-time.Hour)), stamp(now.Add(-time.Second)), 1)
	insertMessage(t, db, "m1", "s-new", "user", now.Add(-time.Minute).Unix())
	insertMessage(t, db, "m2", "s-new", "assistant", now.Add(-20*time.Second).Unix())

	store := SQLiteStore{Path: path}
	ctx := context.Background()

	session, found, err := store.Session(ctx, "/w/api")
	if err != nil || !found {
		t.Fatalf("Session = %+v, %v, %v", session, found, err)
	}
	if session.ID != "s-new" {
		t.Errorf("session id = %s, want the newest non-archived session (its update was written by SQLite's datetime())", session.ID)
	}
	if !session.CreatedAt.Equal(now.Add(-time.Hour)) || !session.UpdatedAt.Equal(now.Add(-time.Second)) {
		t.Errorf("timestamps = %v, %v, want the session's, RFC 3339 and datetime() alike", session.CreatedAt, session.UpdatedAt)
	}
	if session.Tokens != 7000 {
		t.Errorf("tokens = %d, want 7000", session.Tokens)
	}
	if _, found, err := store.Session(ctx, "/w/none"); found || err != nil {
		t.Errorf("Session of a folder without sessions = %v, %v; want no history", found, err)
	}

	message, found, err := store.LastMessage(ctx, "s-new")
	if err != nil || !found {
		t.Fatalf("LastMessage = %+v, %v, %v", message, found, err)
	}
	if message.Role != "assistant" || !message.CreatedAt.Equal(now.Add(-20*time.Second)) {
		t.Errorf("LastMessage = %+v, want the newest assistant message", message)
	}
	if _, found, err := store.LastMessage(ctx, "s-empty"); found || err != nil {
		t.Errorf("LastMessage of a session without messages = %v, %v; want none", found, err)
	}
}

func TestSQLiteStoreMillisecondMessageTimestamps(t *testing.T) {
	path, db := newDatabase(t)
	insertSession(t, db, "s1", "/w/api", stamp(now.Add(-time.Hour)), stamp(now.Add(-time.Minute)), 0)
	insertMessage(t, db, "m1", "s1", "user", now.Add(-30*time.Second).UnixMilli())

	message, found, err := SQLiteStore{Path: path}.LastMessage(context.Background(), "s1")
	if err != nil || !found {
		t.Fatalf("LastMessage = %+v, %v, %v", message, found, err)
	}
	if !message.CreatedAt.Equal(now.Add(-30 * time.Second)) {
		t.Errorf("CreatedAt = %v, want %v (milliseconds read as such)", message.CreatedAt, now.Add(-30*time.Second))
	}
}

func TestSQLiteStoreWithoutDatabase(t *testing.T) {
	store := SQLiteStore{Path: filepath.Join(t.TempDir(), "sessions.db")}
	ctx := context.Background()
	if session, found, err := store.Session(ctx, "/w/api"); session != (Session{}) || found || err != nil {
		t.Errorf("Session = %+v, %v, %v; want no history, not an error", session, found, err)
	}
	if _, found, err := store.LastMessage(ctx, "s1"); found || err != nil {
		t.Errorf("LastMessage = %v, %v; want no history, not an error", found, err)
	}
}

func TestSQLiteStoreForeignSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec(t, db, `CREATE TABLE sessions (id text primary key)`) // a newer Goose renamed the columns

	store := SQLiteStore{Path: path}
	if _, _, err := store.Session(context.Background(), "/w/api"); err == nil {
		t.Error("Session on a foreign schema = no error, want one")
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		value string
		want  time.Time
	}{
		{"", time.Time{}},
		{"2026-11-20T12:00:00Z", time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)},
		{"2026-11-20T12:00:00+01:00", time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)},
		{"2026-11-20 12:00:00", time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)},
		{"1763640000", time.Unix(1763640000, 0)},
		{"1763640000000", time.Unix(1763640000, 0)},
		{"not a time", time.Time{}},
	}
	for _, tt := range tests {
		if got := parseTime(tt.value); !got.Equal(tt.want) {
			t.Errorf("parseTime(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-6c1d"
	path, db := newDatabase(t)
	insertSession(t, db, "s1", "/w/api", stamp(now.Add(-time.Hour)), stamp(now.Add(-time.Second)), 1234)
	exec(t, db, `UPDATE sessions SET name = ?, description = ?, recipe_json = ?, extension_data = ? WHERE id = 's1'`,
		canary, canary, canary, canary)
	insertMessage(t, db, "m1", "s1", "user", now.Add(-time.Minute).Unix())
	insertMessage(t, db, "m2", "s1", "assistant", now.Add(-20*time.Second).Unix())
	exec(t, db, `UPDATE messages SET content_json = ?, metadata_json = ? WHERE id IN ('m1', 'm2')`, canary, canary)

	source := &Source{
		Processes: &fakeProcesses{
			{PID: 31, Name: "goose", Dir: "/w/api", StartedAt: now.Add(-time.Hour)},
		},
		Store: SQLiteStore{Path: path},
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Fatalf("sessions = %+v, want the session read from its database, waiting", sessions)
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

// newDatabase creates Goose's tables in a temporary database and opens it for
// writing, the way Goose itself would.
func newDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(250)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	exec(t, db, `CREATE TABLE sessions (
        id text primary key, name text not null default '', description text not null default '',
        working_dir text not null, created_at timestamp, updated_at timestamp, extension_data text,
        total_tokens integer, recipe_json text, provider_name text, model_config_json text, archived_at timestamp)`)
	exec(t, db, `CREATE TABLE messages (
        id integer primary key autoincrement, message_id text, session_id text not null,
        role text not null, content_json text not null, created_timestamp integer not null,
        timestamp timestamp, tokens integer, metadata_json text)`)
	return path, db
}

func insertSession(t *testing.T, db *sql.DB, id, dir, created, updated string, tokens int64) {
	t.Helper()
	exec(t, db, `INSERT INTO sessions (id, name, description, working_dir, created_at, updated_at, total_tokens)
        VALUES (?, '', '', ?, ?, ?, ?)`, id, dir, created, updated, tokens)
}

func insertMessage(t *testing.T, db *sql.DB, id, session, role string, created int64) {
	t.Helper()
	exec(t, db, `INSERT INTO messages (message_id, session_id, role, content_json, created_timestamp)
        VALUES (?, ?, ?, '{}', ?)`, id, session, role, created)
}

// stamp is how Goose writes the timestamps it binds: RFC 3339.
func stamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// sqliteStamp is how SQLite's datetime() writes the timestamps Goose's
// in-place updates make: the same instant without the T and the zone.
func sqliteStamp(at time.Time) string { return at.UTC().Format(sqliteDatetime) }

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}
