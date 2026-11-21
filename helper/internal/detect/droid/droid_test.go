package droid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
	"github.com/tonylook/managents/helper/internal/protocol"
)

var now = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// fakeProcesses stands in for the process list.
type fakeProcesses []process.Info

func (f *fakeProcesses) FindByName(_ context.Context, name string) ([]process.Info, error) {
	if name != "droid" {
		return nil, nil
	}
	return *f, nil
}

type fixture struct {
	t      *testing.T
	home   string
	procs  *fakeProcesses
	source *Source
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("FACTORY_HOME_OVERRIDE", "") // the developer's own setting must not leak in
	home := t.TempDir()
	procs := &fakeProcesses{}
	return &fixture{t: t, home: home, procs: procs, source: NewSource(home, procs)}
}

// addProcess registers a running droid process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	*f.procs = append(*f.procs, process.Info{PID: pid, Name: "droid", Dir: dir, StartedAt: started})
}

// addSession writes a session file for dir with the given first line and a
// transcript that follows it, and whose last write is at written.
func (f *fixture) addSession(dir, id string, written time.Time, start map[string]any, transcript ...string) string {
	f.t.Helper()
	if start == nil {
		start = map[string]any{"type": "session_start", "id": id, "title": "the session title",
			"owner": "ann", "cwd": dir}
	}
	data, err := json.Marshal(start)
	if err != nil {
		f.t.Fatal(err)
	}
	lines := append([]string{string(data)}, transcript...)
	path := f.sessionPath(dir, id)
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
	writeMtime(f.t, path, written)
	return path
}

// sessionPath is where Factory keeps a session file of dir.
func (f *fixture) sessionPath(dir, id string) string {
	f.t.Helper()
	return filepath.Join(f.source.SessionsDir, projectKey(resolved(dir)), id+".jsonl")
}

// addSidecar writes the settings sidecar of a session file, whose archivedAt
// is the value given.
func (f *fixture) addSidecar(dir, id string, value any) {
	f.t.Helper()
	data, err := json.Marshal(map[string]any{"model": "claude-opus-4-6", "archivedAt": value})
	if err != nil {
		f.t.Fatal(err)
	}
	f.writeSidecar(dir, id, data)
}

// addRawSidecar writes a sidecar whose fields are the ones given, e.g. one
// that carries no archivedAt at all.
func (f *fixture) addRawSidecar(dir, id string, value any) {
	f.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	f.writeSidecar(dir, id, data)
}

func (f *fixture) writeSidecar(dir, id string, data []byte) {
	f.t.Helper()
	writeFile(f.t, strings.TrimSuffix(f.sessionPath(dir, id), ".jsonl")+".settings.json", string(data))
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
		written time.Time
		want    agent.Status
		wantAge time.Duration
	}{
		{
			name:    "a file written seconds ago is working",
			started: now.Add(-time.Hour),
			written: now.Add(-30 * time.Second),
			want:    agent.StatusWorking,
			wantAge: 30 * time.Second,
		},
		{
			name:    "a quiet file is waiting: a permission prompt looks no different",
			started: now.Add(-time.Hour),
			written: now.Add(-5 * time.Minute),
			want:    agent.StatusWaiting,
			wantAge: 5 * time.Minute,
		},
		{
			name:    "a write from a previous run is not working",
			started: now.Add(-time.Hour),
			written: now.Add(-3 * time.Hour),
			want:    agent.StatusWaiting,
			wantAge: time.Hour,
		},
		{
			name:    "a file untouched for hours is idle",
			started: now.Add(-5 * time.Hour),
			written: now.Add(-3 * time.Hour),
			want:    agent.StatusIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := "/work/payments-api"
			f.addProcess(100, dir, tt.started)
			f.addSession(dir, "session-a", tt.written, nil,
				`{"type":"message","timestamp":1763545200,"message":{"role":"user","content":"the prompt text"}}`,
			)

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
	dir := "/work/agent-lights"
	f.addProcess(4242, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-abc", now.Add(-42*time.Second), nil)

	s := f.sessions()[0]

	if s.ID != "droid:session-abc" || s.Kind != agent.KindDroid || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if s.StartedAt.After(s.Since) {
		t.Errorf("startedAt %v is after the last write %v", s.StartedAt, s.Since)
	}
	if runtime.GOOS == "darwin" && !s.StartedAt.After(now.Add(-time.Hour)) {
		t.Errorf("startedAt = %v, want the file's creation, not its last write", s.StartedAt)
	}
	if got := s.Age(now); got != 42*time.Second {
		t.Errorf("age = %v, want 42s", got)
	}
}

func TestArchivedSessionsGetNoCard(t *testing.T) {
	tests := []struct {
		name     string
		sidecar  bool
		setTo    any
		archived bool
	}{
		{"no sidecar", false, nil, false},
		{"archivedAt set", true, "2026-11-18T09:00:00Z", true},
		{"null archivedAt", true, nil, false},
		{"zero archivedAt", true, 0, false},
		{"empty archivedAt", true, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := "/work/payments-api"
			f.addProcess(9, dir, now.Add(-time.Hour))
			f.addSession(dir, "session-a", now.Add(-time.Minute), nil)
			if tt.sidecar {
				f.addSidecar(dir, "session-a", tt.setTo)
			}

			sessions := f.sessions()

			if tt.archived {
				if len(sessions) != 1 || sessions[0].ID != "droid:9" || sessions[0].Status != agent.StatusWaiting {
					t.Errorf("sessions = %+v, want only the process's waiting card: the archived session is hidden", sessions)
				}
				return
			}
			if len(sessions) != 1 || sessions[0].ID != "droid:session-a" {
				t.Errorf("sessions = %+v, want the open session", sessions)
			}
		})
	}
}

func TestNoSessionFileYetIsWaitingCard(t *testing.T) {
	f := newFixture(t)
	started := now.Add(-time.Hour)
	f.addProcess(7, "/work/website", started)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "droid:7" || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card for the process", sessions)
	}
	if !sessions[0].Since.Equal(started) {
		t.Errorf("since = %v, want the process start %v", sessions[0].Since, started)
	}
}

func TestSkipsExitedProcessesAndOtherFolders(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(1, dir, now.Add(-2*time.Hour))
	f.addProcess(2, dir, now.Add(-time.Hour)) // two processes, one card
	f.addSession(dir, "session-a", now.Add(-time.Minute), nil)
	f.addSession("/work/exited", "session-b", now.Add(-time.Minute), nil) // no process: exited
	f.addProcess(3, "", now.Add(-time.Hour))                              // no working directory: not a session
	other := "/work/other (v2)"
	f.addProcess(4, other, now.Add(-time.Hour))
	f.addSession(other, "session-c", now.Add(-time.Minute),
		map[string]any{"type": "session_start", "id": "session-c", "cwd": "/work/other"},
	) // first line carries another folder's cwd: a waiting card for the process

	sessions := f.sessions()

	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want the payments-api card and the waiting card of other (v2)", sessions)
	}
	if sessions[0].ID != "droid:session-a" || sessions[0].Dir != dir {
		t.Errorf("first card = %+v, want payments-api's session", sessions[0])
	}
	if sessions[1].ID != "droid:4" || sessions[1].Status != agent.StatusWaiting {
		t.Errorf("second card = %+v, want a waiting card for pid 4", sessions[1])
	}
}

func TestNewestSessionFileIsTheActiveOne(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(5, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-old", now.Add(-3*time.Hour), nil)
	f.addSession(dir, "session-new", now.Add(-5*time.Minute), nil)

	s := f.sessions()[0]

	if s.ID != "droid:session-new" {
		t.Errorf("id = %s, want the newest session file", s.ID)
	}
	if s.Status != agent.StatusWaiting || s.Age(now) != 5*time.Minute {
		t.Errorf("session = %+v, want waiting since the newest file's last write", s)
	}
}

func TestSymlinkedWorkingDirectory(t *testing.T) {
	f := newFixture(t)
	real := filepath.Join(f.home, "workspace", "website")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	// The recorded cwd is the real path, as droid's own project key is.
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.home, "link-to-website")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f.addProcess(6, link, now.Add(-time.Hour)) // droid derives the key from the real path
	f.addSession(resolved, "session-a", now.Add(-time.Minute), nil)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "droid:session-a" {
		t.Errorf("sessions = %+v, want the session of the symlinked folder", sessions)
	}
}

func TestLegacyLayoutsAreSkipped(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(8, dir, now.Add(-time.Hour))
	// The legacy layouts: sessions straight in sessions/, and the btw sessions.
	writeFile(f.t, filepath.Join(f.source.SessionsDir, "session-flat.jsonl"),
		`{"type":"session_start","id":"session-flat","cwd":"`+dir+`"}`+"\n")
	writeFile(f.t, filepath.Join(f.source.SessionsDir, "btw", "session-btw.jsonl"),
		`{"type":"session_start","id":"session-btw","cwd":"`+dir+`"}`+"\n")
	f.addSession(dir, "session-a", now.Add(-time.Minute), nil)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "droid:session-a" {
		t.Errorf("sessions = %+v, want only the project-keyed session", sessions)
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-4a9f"
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(13, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-private", now.Add(-time.Minute),
		map[string]any{"type": "session_start", "id": "session-private", "title": canary,
			"owner": canary, "cwd": dir},
		`{"type":"message","timestamp":1763545200,"message":{"role":"user","content":"`+canary+`"}}`,
		`{"type":"message","timestamp":1763545260,"message":{"role":"assistant","content":"`+canary+`"}}`,
		`{"type":"tool_result","timestamp":1763545320,"output":"`+canary+`"}`,
	)
	// A sidecar whose other fields hold conversation text, but that archives
	// nothing: the session must still be read, and its title and owner must
	// not leak.
	f.addRawSidecar(dir, "session-private", map[string]any{"model": "claude-opus-4-6", "note": canary})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "droid:session-private" || sessions[0].Status != agent.StatusWorking {
		t.Fatalf("sessions = %+v, want the session read from its file, working", sessions)
	}
	for _, s := range sessions {
		if rendered := fmt.Sprintf("%+v", s); strings.Contains(rendered, canary) {
			t.Errorf("conversation content leaked into the session fields: %s", rendered)
		}
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

func TestProjectKey(t *testing.T) {
	tests := []struct{ dir, want string }{
		{"/Users/alice/code/myapp", "-Users-alice-code-myapp"},
		{"/work/payments-api", "-work-payments-api"},
		{"/", "-"},
		// Windows spellings fold to a legal directory name; the drive colon
		// cannot survive in a directory name there.
		{`C:\Users\alice\code\myapp`, "-C--Users-alice-code-myapp"},
		{`c:\Users\alice\repo`, "-c--Users-alice-repo"},
	}
	for _, tt := range tests {
		if got := projectKey(tt.dir); got != tt.want {
			t.Errorf("projectKey(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestFactoryHomeOverride(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "factory-home")
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"default", "", filepath.Join(home, ".factory", "sessions")},
		{"FACTORY_HOME_OVERRIDE", custom, filepath.Join(custom, "sessions")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FACTORY_HOME_OVERRIDE", tt.env)
			if got := NewSource(home, &fakeProcesses{}).SessionsDir; got != tt.want {
				t.Errorf("sessions dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingSessionsDirIsNotAnError(t *testing.T) {
	f := newFixture(t)
	f.addProcess(14, "/work/website", now.Add(-time.Hour))
	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}
	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card", sessions)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMtime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
