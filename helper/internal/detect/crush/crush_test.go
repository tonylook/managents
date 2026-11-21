package crush

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

// fakeProcesses stands in for the process list and the liveness check.
type fakeProcesses struct {
	procs []process.Info
	// alive answers StartTime: a pid in it is running.
	alive map[int]bool
}

func (f *fakeProcesses) FindByName(_ context.Context, name string) ([]process.Info, error) {
	if name != "crush" {
		return nil, nil
	}
	return f.procs, nil
}

func (f *fakeProcesses) StartTime(_ context.Context, pid int) (time.Time, bool) {
	if f.alive[pid] {
		return now.Add(-3 * time.Hour), true
	}
	return time.Time{}, false
}

// fakeStore stands in for a project database.
type fakeStore struct {
	session  Session
	found    bool
	message  Message
	msgFound bool
	err      error
	// asked records the data directory the store was last asked about.
	asked string
}

func (f *fakeStore) Session(_ context.Context, dataDir string) (Session, bool, error) {
	f.asked = dataDir
	if f.err != nil {
		return Session{}, false, f.err
	}
	return f.session, f.found, nil
}

func (f *fakeStore) LastMessage(_ context.Context, dataDir, _ string) (Message, bool, error) {
	f.asked = dataDir
	if f.err != nil {
		return Message{}, false, f.err
	}
	return f.message, f.msgFound, nil
}

type fixture struct {
	t         *testing.T
	home      string
	procs     *fakeProcesses
	store     *fakeStore
	source    *Source
	globalDir string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("CRUSH_GLOBAL_DATA", "") // the developer's own setting must not leak in
	t.Setenv("XDG_DATA_HOME", "")
	home := t.TempDir()
	procs := &fakeProcesses{alive: make(map[int]bool)}
	store := &fakeStore{}
	globalDir := filepath.Join(home, ".local", "share", "crush")
	source := &Source{Processes: procs, Store: store, GlobalDir: globalDir}
	return &fixture{t: t, home: home, procs: procs, store: store, source: source, globalDir: globalDir}
}

// addProcess registers a running crush process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	f.procs.procs = append(f.procs.procs, process.Info{PID: pid, Name: "crush", Dir: dir, StartedAt: started})
}

// project makes a working directory under the temporary home, so a test can
// put a .crush directory in it.
func (f *fixture) project(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "work", name)
	mkdir(f.t, dir)
	return dir
}

// setSession fills the store's answer: the project's active session and its
// newest message. A pid above zero writes the data directory's lock file, with
// a running process when alive.
func (f *fixture) setSession(dataDir string, pid int, alive bool, session Session, message Message) {
	f.t.Helper()
	f.store.session, f.store.found = session, true
	f.store.message, f.store.msgFound = message, true
	if pid > 0 {
		writeFile(f.t, filepath.Join(dataDir, "crush.lock"), fmt.Sprintf(`{"pid":%d}`, pid))
		if alive {
			f.procs.alive[pid] = true
		}
	}
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
		name     string
		started  time.Time
		lockPID  int // 0: no lock file
		lockDead bool
		message  Message
		want     agent.Status
		wantAge  time.Duration
	}{
		{
			name:    "an unfinished assistant message is working",
			started: now.Add(-time.Hour),
			lockPID: 4100,
			message: Message{Role: "assistant", CreatedAt: now.Add(-40 * time.Second), PromptedAt: now.Add(-time.Minute)},
			want:    agent.StatusWorking,
			wantAge: time.Minute,
		},
		{
			name:    "a prompt without its reply is working",
			started: now.Add(-time.Hour),
			lockPID: 4100,
			message: Message{Role: "user", CreatedAt: now.Add(-30 * time.Second), PromptedAt: now.Add(-30 * time.Second)},
			want:    agent.StatusWorking,
			wantAge: 30 * time.Second,
		},
		{
			name:    "a finished assistant message is waiting",
			started: now.Add(-time.Hour),
			lockPID: 4100,
			message: Message{Role: "assistant", CreatedAt: now.Add(-time.Minute), FinishedAt: now.Add(-20 * time.Second)},
			want:    agent.StatusWaiting,
			wantAge: 20 * time.Second,
		},
		{
			name:     "an unfinished message behind a stale lock is a run that died mid-turn",
			started:  now.Add(-time.Hour),
			lockPID:  4100,
			lockDead: true,
			message:  Message{Role: "assistant", CreatedAt: now.Add(-time.Minute), PromptedAt: now.Add(-2 * time.Minute)},
			want:     agent.StatusWaiting,
		},
		{
			// A repository can commit a lock naming any live pid; only the
			// card's own process holds the lock over its data directory.
			name:    "a lock naming another process is not held",
			started: now.Add(-time.Hour),
			lockPID: 5999,
			message: Message{Role: "user", CreatedAt: now.Add(-30 * time.Second), PromptedAt: now.Add(-30 * time.Second)},
			want:    agent.StatusWaiting,
		},
		{
			name:    "a committed lock naming pid 1 is not held",
			started: now.Add(-time.Hour),
			lockPID: 1,
			message: Message{Role: "user", CreatedAt: now.Add(-30 * time.Second), PromptedAt: now.Add(-30 * time.Second)},
			want:    agent.StatusWaiting,
		},
		{
			name:    "no lock file is not working either",
			started: now.Add(-time.Hour),
			message: Message{Role: "user", CreatedAt: now.Add(-30 * time.Second), PromptedAt: now.Add(-30 * time.Second)},
			want:    agent.StatusWaiting,
		},
		{
			name:    "a finished turn untouched for hours is idle",
			started: now.Add(-4 * time.Hour),
			lockPID: 4100,
			message: Message{Role: "assistant", CreatedAt: now.Add(-3 * time.Hour), FinishedAt: now.Add(-3 * time.Hour)},
			want:    agent.StatusIdle,
		},
		{
			name:    "an unfinished turn that hung for hours is idle",
			started: now.Add(-4 * time.Hour),
			lockPID: 4100,
			message: Message{Role: "user", CreatedAt: now.Add(-3 * time.Hour), PromptedAt: now.Add(-3 * time.Hour)},
			want:    agent.StatusIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := f.project("payments-api")
			f.addProcess(4100, dir, tt.started)
			dataDir := filepath.Join(dir, ".crush")
			mkdir(f.t, dataDir)
			f.setSession(dataDir, tt.lockPID, !tt.lockDead,
				Session{ID: "session-a", CreatedAt: tt.started}, tt.message)

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

func TestMessageFromPreviousRunIsNotWorking(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	started := now.Add(-time.Hour)
	f.addProcess(4100, dir, started)
	dataDir := filepath.Join(dir, ".crush")
	mkdir(f.t, dataDir)
	f.setSession(dataDir, 4100, true, Session{ID: "session-a", CreatedAt: now.Add(-3 * time.Hour)},
		Message{Role: "user", CreatedAt: now.Add(-3 * time.Hour), PromptedAt: now.Add(-3 * time.Hour)})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Fatalf("sessions = %+v, want one waiting card: the prompt belongs to a previous run", sessions)
	}
	if !sessions[0].Since.Equal(started) {
		t.Errorf("since = %v, want the process start %v", sessions[0].Since, started)
	}
}

func TestSessionFields(t *testing.T) {
	f := newFixture(t)
	dir := f.project("agent-lights")
	started := now.Add(-time.Hour)
	f.addProcess(4242, dir, started)
	dataDir := filepath.Join(dir, ".crush")
	mkdir(f.t, dataDir)
	opened := now.Add(-50 * time.Minute)
	f.setSession(dataDir, 4242, true,
		Session{ID: "session-abc", CreatedAt: opened, Tokens: 12_000},
		Message{Role: "assistant", CreatedAt: now.Add(-time.Minute), FinishedAt: now.Add(-42 * time.Second)})

	s := f.sessions()[0]

	if s.ID != "crush:session-abc" || s.Kind != agent.KindCrush || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(opened) {
		t.Errorf("startedAt = %v, want the session's start %v", s.StartedAt, opened)
	}
	if s.Context == nil || s.Context.Used != 12_000 || s.Context.Limit != 0 {
		t.Errorf("context = %+v, want 12000 used, no limit (crush stores no window size)", s.Context)
	}
	if got := s.Age(now); got != 42*time.Second {
		t.Errorf("age = %v, want 42s", got)
	}
}

func TestNoSessionYetIsWaitingCard(t *testing.T) {
	f := newFixture(t)
	started := now.Add(-time.Hour)
	f.addProcess(7, "/work/website", started)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "crush:7" || sessions[0].Status != agent.StatusWaiting {
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
	dataDir := filepath.Join(dir, ".crush")
	mkdir(f.t, dataDir)
	f.setSession(dataDir, 0, false, Session{ID: "session-a", CreatedAt: now.Add(-time.Hour)},
		Message{Role: "assistant", FinishedAt: now.Add(-time.Minute)})
	f.addProcess(3, "", now.Add(-time.Hour)) // no working directory: not a session

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "crush:session-a" {
		t.Fatalf("sessions = %+v, want one card for payments-api", sessions)
	}
}

func TestDataDirFromRegistry(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addProcess(21, dir, now.Add(-time.Hour))
	dataDir := filepath.Join(t.TempDir(), "crush-data") // outside the project
	mkdir(f.t, dataDir)
	writeFile(f.t, filepath.Join(f.globalDir, "projects.json"),
		`{"projects":[{"path":`+quote(dir)+`,"data_dir":`+quote(dataDir)+`,"last_accessed":1}]}`)
	f.setSession(dataDir, 0, false, Session{ID: "session-reg", CreatedAt: now.Add(-time.Hour)},
		Message{Role: "assistant", FinishedAt: now.Add(-time.Minute)})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "crush:session-reg" {
		t.Fatalf("sessions = %+v, want the session read from the registered data directory", sessions)
	}
	if f.store.asked != dataDir {
		t.Errorf("store asked for %q, want the registered data directory %q", f.store.asked, dataDir)
	}
}

func TestDataDirFromDotCrushAboveTheProject(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "app")
	mkdir(f.t, filepath.Join(root, ".crush"))
	f.addProcess(22, sub, now.Add(-time.Hour))
	f.setSession(filepath.Join(root, ".crush"), 0, false,
		Session{ID: "session-up", CreatedAt: now.Add(-time.Hour)},
		Message{Role: "assistant", FinishedAt: now.Add(-time.Minute)})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "crush:session-up" {
		t.Fatalf("sessions = %+v, want the session of the ancestor data directory", sessions)
	}
	if f.store.asked != filepath.Join(root, ".crush") {
		t.Errorf("store asked for %q, want the nearest .crush above the working directory", f.store.asked)
	}
}

func TestNoDataDirectoryYet(t *testing.T) {
	f := newFixture(t)
	f.addProcess(23, t.TempDir(), now.Add(-time.Hour)) // a project without .crush or a registry entry

	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}
	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card", sessions)
	}
}

func TestUnreadableRegistryFallsBackToDotCrush(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addProcess(24, dir, now.Add(-time.Hour))
	writeFile(f.t, filepath.Join(f.globalDir, "projects.json"), "{not json")
	dataDir := filepath.Join(dir, ".crush")
	mkdir(f.t, dataDir)
	f.setSession(dataDir, 0, false, Session{ID: "session-a", CreatedAt: now.Add(-time.Hour)},
		Message{Role: "assistant", FinishedAt: now.Add(-time.Minute)})

	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatalf("err = %v, want none: a bad registry is not an error", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "crush:session-a" {
		t.Errorf("sessions = %+v, want the session of the .crush directory", sessions)
	}
}

func TestStoreErrorIsReturnedButCardShown(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addProcess(25, dir, now.Add(-time.Hour))
	dataDir := filepath.Join(dir, ".crush")
	mkdir(f.t, dataDir)
	f.store.err = fmt.Errorf("schema changed")

	sessions, err := f.source.Sessions(context.Background(), now)
	if err == nil {
		t.Fatal("err = nil, want the store error")
	}
	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card despite the error", sessions)
	}
}

func TestHomeOverride(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "crush-global")
	xdg := filepath.Join(t.TempDir(), "xdg")
	tests := []struct {
		name string
		env  string
		xdg  string
		want string
	}{
		{"default", "", "", filepath.Join(home, ".local", "share", "crush")},
		{"XDG_DATA_HOME", "", xdg, filepath.Join(xdg, "crush")},
		{"CRUSH_GLOBAL_DATA", custom, xdg, custom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CRUSH_GLOBAL_DATA", tt.env)
			t.Setenv("XDG_DATA_HOME", tt.xdg)
			if got := DefaultGlobalDataDir(home); got != tt.want {
				t.Errorf("global data dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLiteStore(t *testing.T) {
	path, db := newDatabase(t)
	at := now.Unix()
	insertSession(t, db, "s-old", at-90000, at-80000, 100, 20)
	insertSession(t, db, "title-s-old", at-70000, at-60000, 0, 0) // a title-generation session
	insertSession(t, db, "msg1$$tool1", at-60000, at-50000, 0, 0) // an agent-tool session
	insertSession(t, db, "s-new", at-60000, at-10000, 4200, 800)
	insertMessage(t, db, "m1", "s-new", "user", at-50000, at-50000, sql.NullInt64{})
	insertMessage(t, db, "m2", "s-new", "assistant", at-40000, at-20000, sqlNull(at-20000))
	insertMessage(t, db, "m3", "s-new", "user", at-15000, at-15000, sql.NullInt64{})

	store := SQLiteStore{}
	ctx := context.Background()
	dataDir := filepath.Dir(path)

	session, found, err := store.Session(ctx, dataDir)
	if err != nil || !found {
		t.Fatalf("Session = %+v, %v, %v", session, found, err)
	}
	if session.ID != "s-new" {
		t.Errorf("session id = %s, want the newest non-special session", session.ID)
	}
	if !session.CreatedAt.Equal(time.Unix(at-60000, 0)) || !session.UpdatedAt.Equal(time.Unix(at-10000, 0)) {
		t.Errorf("timestamps = %v, %v, want the session's", session.CreatedAt, session.UpdatedAt)
	}
	if session.Tokens != 5000 {
		t.Errorf("tokens = %d, want prompt + completion = 5000", session.Tokens)
	}
	if _, found, err := store.Session(ctx, filepath.Join(t.TempDir(), "nowhere")); found || err != nil {
		t.Errorf("Session of a project without a database = %v, %v; want no history, not an error", found, err)
	}

	message, found, err := store.LastMessage(ctx, dataDir, "s-new")
	if err != nil || !found {
		t.Fatalf("LastMessage = %+v, %v, %v", message, found, err)
	}
	want := Message{
		Role: "user", CreatedAt: time.Unix(at-15000, 0),
		PromptedAt: time.Unix(at-15000, 0), FinishedAt: time.Time{},
	}
	if message != want {
		t.Errorf("LastMessage = %+v, want %+v", message, want)
	}
	if _, found, err := store.LastMessage(ctx, dataDir, "s-empty"); found || err != nil {
		t.Errorf("LastMessage of a session without messages = %v, %v; want none", found, err)
	}
}

func TestSQLiteStoreForeignSchema(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".crush")
	mkdir(t, dataDir)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "crush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec(t, db, `CREATE TABLE sessions (id text primary key)`) // a newer Crush renamed the columns

	store := SQLiteStore{}
	if _, _, err := store.Session(context.Background(), dataDir); err == nil {
		t.Error("Session on a foreign schema = no error, want one")
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-2b8e"
	path, db := newDatabase(t)
	at := now.Unix()
	insertSession(t, db, "s1", at-3600, at-10, 1234, 567)
	exec(t, db, `UPDATE sessions SET title = ?, todos = ? WHERE id = 's1'`, canary, canary)
	insertMessage(t, db, "m1", "s1", "user", at-50, at-50, sql.NullInt64{})
	insertMessage(t, db, "m2", "s1", "assistant", at-40, at-30, sql.NullInt64{})
	exec(t, db, `UPDATE messages SET parts = ? WHERE id IN ('m1', 'm2')`, canary)
	exec(t, db, `INSERT INTO files (path, content, version, session_id) VALUES ('main.go', ?, 'v1', 's1')`, canary)
	exec(t, db, `INSERT INTO read_files (session_id, path) VALUES ('s1', ?)`, canary)

	project := filepath.Dir(filepath.Dir(path)) // the project directory holding .crush
	source := &Source{
		Processes: &fakeProcesses{
			procs: []process.Info{{PID: 31, Name: "crush", Dir: project, StartedAt: now.Add(-time.Hour)}},
			alive: map[int]bool{31: true},
		},
		Store:     SQLiteStore{},
		GlobalDir: filepath.Join(t.TempDir(), "no-registry"),
	}
	writeFile(t, filepath.Join(project, ".crush", "crush.lock"), `{"pid":31}`)

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWorking {
		t.Fatalf("sessions = %+v, want the session read from its database, working", sessions)
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

// newDatabase creates Crush's tables in a temporary project data directory and
// opens the database for writing, the way Crush itself would.
func newDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), ".crush")
	mkdir(t, dataDir)
	path := filepath.Join(dataDir, "crush.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(250)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	exec(t, db, `CREATE TABLE sessions (
        id text primary key, parent_session_id text, title text, message_count integer,
        prompt_tokens integer, completion_tokens integer, cost real,
        updated_at integer, created_at integer, todos text, channel text)`)
	exec(t, db, `CREATE TABLE messages (
        id text primary key, session_id text not null, role text not null, parts text,
        model text, created_at integer not null, updated_at integer not null,
        finished_at integer, provider text, model_name text)`)
	exec(t, db, `CREATE TABLE files (path text, content text, version text, session_id text)`)
	exec(t, db, `CREATE TABLE read_files (session_id text, path text)`)
	return path, db
}

func insertSession(t *testing.T, db *sql.DB, id string, created, updated, prompt, completion int64) {
	t.Helper()
	exec(t, db, `INSERT INTO sessions (id, title, prompt_tokens, completion_tokens, created_at, updated_at)
        VALUES (?, '', ?, ?, ?, ?)`, id, prompt, completion, created, updated)
}

func insertMessage(t *testing.T, db *sql.DB, id, session, role string, created, updated int64, finished sql.NullInt64) {
	t.Helper()
	exec(t, db, `INSERT INTO messages (id, session_id, role, parts, created_at, updated_at, finished_at)
        VALUES (?, ?, ?, '{}', ?, ?, ?)`, id, session, role, created, updated, finished)
}

func sqlNull(value int64) sql.NullInt64 { return sql.NullInt64{Valid: true, Int64: value} }

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// quote renders a path as a JSON string.
func quote(path string) string {
	data, err := json.Marshal(path)
	if err != nil {
		return `""`
	}
	return string(data)
}
