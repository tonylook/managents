package omp

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
	"github.com/tonylook/managents/helper/internal/detect/pi"
	"github.com/tonylook/managents/helper/internal/process"
	"github.com/tonylook/managents/helper/internal/protocol"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// fakeProcesses stands in for the process list.
type fakeProcesses []process.Info

func (f *fakeProcesses) FindByName(_ context.Context, _ string) ([]process.Info, error) {
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
	// The developer's own settings must not leak in.
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OMP_PROFILE", "")
	t.Setenv("PI_PROFILE", "")
	home := t.TempDir()
	procs := &fakeProcesses{}
	source := NewSource(home, procs)
	// The temp root usually sits outside home; keep it that way here, so the
	// fixture's sessions are named home-relative like real ones. t.TempDir
	// always nests inside the OS temp dir, where omp would name them
	// temp-relative.
	source.TempRoot = t.TempDir()
	return &fixture{t: t, home: source.Home, procs: procs, source: source}
}

// addProcess registers a running omp process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	*f.procs = append(*f.procs, process.Info{PID: pid, Name: "omp", Dir: dir, StartedAt: started})
}

// addSession writes a session file for dir, in the folder omp names after it,
// whose entries are the fields given (a session header first, unless the
// caller passes their own), and whose last write is at written.
func (f *fixture) addSession(dir, id string, written time.Time, entries ...map[string]any) {
	f.t.Helper()
	if len(entries) == 0 || entries[0]["type"] != "session" {
		header := map[string]any{
			"type": "session", "version": 3, "id": id,
			"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": dir,
		}
		entries = append([]map[string]any{header}, entries...)
	}
	f.addRawSession(dir, id, written, entries...)
}

// addRawSession writes a session file whose entries are used as given.
func (f *fixture) addRawSession(dir, id string, written time.Time, entries ...map[string]any) {
	f.t.Helper()
	f.writeFile(f.source.SessionsRoots[0], f.canonicalFolder(dir), "2026-11-20T11-00-00-000Z_"+id+".jsonl", written, entries)
}

// writeFile serializes entries as JSON lines under sessionsRoot/folder/name.
func (f *fixture) writeFile(sessionsRoot, folder, name string, written time.Time, entries []map[string]any) {
	f.t.Helper()
	var lines []string
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			f.t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	path := filepath.Join(sessionsRoot, folder, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		f.t.Fatal(err)
	}
}

// canonicalFolder is the first folder name omp uses for dir.
func (f *fixture) canonicalFolder(dir string) string {
	f.t.Helper()
	folders := sessionFolders(dir, f.source.Home, f.source.TempRoot)
	if len(folders) == 0 {
		f.t.Fatal("no session folders for " + dir)
	}
	return folders[0]
}

// user, assistant and toolResult are the message entries omp appends during a
// turn.
func user(at time.Time, fields map[string]any) map[string]any {
	message := map[string]any{"role": "user", "content": "the prompt text"}
	for k, v := range fields {
		message[k] = v
	}
	return map[string]any{"type": "message", "timestamp": at.Format(time.RFC3339Nano), "message": message}
}

func assistant(at time.Time, stopReason string, fields map[string]any) map[string]any {
	message := map[string]any{"role": "assistant", "stopReason": stopReason, "content": "the reply text"}
	for k, v := range fields {
		message[k] = v
	}
	return map[string]any{"type": "message", "timestamp": at.Format(time.RFC3339Nano), "message": message}
}

func toolResult(at time.Time) map[string]any {
	return map[string]any{"type": "message", "timestamp": at.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "toolResult", "output": "the tool output"}}
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
	usage := map[string]any{"usage": map[string]any{"input": 40_000, "output": 2_000, "totalTokens": 42_000}}
	tests := []struct {
		name    string
		started time.Time
		entries []map[string]any
		want    agent.Status
		wantAge time.Duration
	}{
		{
			name:    "a prompt without its reply is working",
			started: now.Add(-time.Hour),
			entries: []map[string]any{user(now.Add(-30*time.Second), nil)},
			want:    agent.StatusWorking,
			wantAge: 30 * time.Second,
		},
		{
			name:    "a finished turn is waiting",
			started: now.Add(-time.Hour),
			entries: []map[string]any{
				user(now.Add(-time.Minute), nil),
				assistant(now.Add(-20*time.Second), "stop", usage),
			},
			want:    agent.StatusWaiting,
			wantAge: 20 * time.Second,
		},
		{
			name:    "a tool call without its result is a permission prompt",
			started: now.Add(-time.Hour),
			entries: []map[string]any{
				user(now.Add(-time.Minute), nil),
				assistant(now.Add(-20*time.Second), "toolUse", nil),
			},
			want:    agent.StatusWaiting,
			wantAge: 20 * time.Second,
		},
		{
			name:    "a tool result is a turn in progress, from the prompt",
			started: now.Add(-time.Hour),
			entries: []map[string]any{
				user(now.Add(-time.Minute), nil),
				assistant(now.Add(-40*time.Second), "toolUse", nil),
				toolResult(now.Add(-30 * time.Second)),
			},
			want:    agent.StatusWorking,
			wantAge: time.Minute,
		},
		{
			name:    "a turn that ended in an error is error",
			started: now.Add(-time.Hour),
			entries: []map[string]any{
				user(now.Add(-time.Minute), nil),
				assistant(now.Add(-20*time.Second), "error", nil),
			},
			want: agent.StatusError,
		},
		{
			name:    "an interrupted turn is waiting, not error",
			started: now.Add(-time.Hour),
			entries: []map[string]any{
				user(now.Add(-time.Minute), nil),
				assistant(now.Add(-20*time.Second), "aborted", nil),
			},
			want: agent.StatusWaiting,
		},
		{
			name:    "a finished turn untouched for hours is idle",
			started: now.Add(-4 * time.Hour),
			entries: []map[string]any{
				user(now.Add(-3*time.Hour), nil),
				assistant(now.Add(-3*time.Hour), "stop", nil),
			},
			want: agent.StatusIdle,
		},
		{
			name:    "an entry from a previous run is not working",
			started: now.Add(-time.Hour),
			entries: []map[string]any{user(now.Add(-3*time.Hour), nil)},
			want:    agent.StatusWaiting,
		},
		{
			name:    "a prompt without its reply that hung for hours is idle",
			started: now.Add(-4 * time.Hour),
			entries: []map[string]any{user(now.Add(-3*time.Hour), nil)},
			want:    agent.StatusIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := f.home + "/work/payments-api"
			f.addProcess(100, dir, tt.started)
			f.addSession(dir, "sess-a", now.Add(-time.Second), tt.entries...)

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
	dir := f.home + "/work/agent-lights"
	started := now.Add(-2 * time.Hour)
	f.addProcess(4242, dir, now.Add(-time.Hour))
	header := map[string]any{"type": "session", "version": 3, "id": "sess-uuid-1",
		"timestamp": started.Format(time.RFC3339Nano), "cwd": dir}
	f.addSession(dir, "ignored", now.Add(-time.Second), header,
		user(now.Add(-42*time.Second), nil),
		assistant(now.Add(-20*time.Second), "stop", nil),
	)

	s := f.sessions()[0]

	if s.ID != "omp:sess-uuid-1" || s.Kind != agent.KindOmp || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(started) {
		t.Errorf("startedAt = %v, want %v (the session header's timestamp)", s.StartedAt, started)
	}
	if got := s.Age(now); got != 20*time.Second {
		t.Errorf("age = %v, want 20s since the turn ended", got)
	}
}

// The title slot omp writes ahead of the header holds the conversation title;
// the header behind it is still found, and the slot's text is never decoded.
func TestTitleSlotBeforeHeader(t *testing.T) {
	f := newFixture(t)
	dir := f.home + "/work/payments-api"
	f.addProcess(21, dir, now.Add(-time.Hour))
	slot := map[string]any{"type": "title", "v": 1, "title": "the title text",
		"updatedAt": now.Add(-time.Hour).Format(time.RFC3339Nano),
		"pad":       strings.Repeat(" ", 100)}
	header := map[string]any{"type": "session", "version": 3, "id": "sess-titled",
		"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": dir}
	f.addRawSession(dir, "titled", now.Add(-time.Second), slot, header,
		user(now.Add(-30*time.Second), nil),
		assistant(now.Add(-10*time.Second), "stop", nil),
	)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "omp:sess-titled" || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want the titled session read past the title slot", sessions)
	}
}

// An entry written off the active branch (activeLeafId) does not decide where
// the turn stands: omp's loader replays it against the recorded leaf.
func TestOffBranchEntryDoesNotDecideTheTurn(t *testing.T) {
	f := newFixture(t)
	dir := f.home + "/work/payments-api"
	f.addProcess(22, dir, now.Add(-time.Hour))
	f.addSession(dir, "sess-branch", now.Add(-time.Second),
		user(now.Add(-time.Minute), nil),
		assistant(now.Add(-20*time.Second), "stop", nil),
		map[string]any{"type": "message", "timestamp": now.Add(-5 * time.Second).Format(time.RFC3339Nano),
			"activeLeafId": "earlier-entry", "message": map[string]any{"role": "user", "content": "the prompt text"}},
	)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want waiting: the off-branch prompt is not the leaf", sessions)
	}
}

func TestSessionsInProfiles(t *testing.T) {
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OMP_PROFILE", "")
	t.Setenv("PI_PROFILE", "")
	home := t.TempDir()
	for _, name := range []string{"work", "personal"} {
		if err := os.MkdirAll(filepath.Join(home, ".omp", "profiles", name, "agent", "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	procs := &fakeProcesses{}
	source := NewSource(home, procs)
	source.TempRoot = t.TempDir()
	f := &fixture{t: t, home: source.Home, procs: procs, source: source}
	first := f.home + "/work/website"
	second := f.home + "/work/payments-api"
	f.addProcess(30, first, now.Add(-time.Hour))
	f.addProcess(31, second, now.Add(-time.Hour))
	f.writeFile(f.source.SessionsRoots[0], f.canonicalFolder(first), "2026-11-20T10-00-00-000Z_sess-main.jsonl",
		now.Add(-time.Minute), []map[string]any{
			{"type": "session", "version": 3, "id": "sess-main",
				"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": first},
			user(now.Add(-30*time.Second), nil),
			assistant(now.Add(-time.Second), "stop", nil),
		})
	for i, name := range []string{"work", "personal"} {
		// The personal profile's file is written later: it wins the folder.
		written := now.Add(-time.Minute)
		if name == "personal" {
			written = now.Add(-30 * time.Second)
		}
		f.writeFile(f.source.SessionsRoots[i+1], f.canonicalFolder(second), "2026-11-20T10-30-00-000Z_sess-p.jsonl",
			written, []map[string]any{
				{"type": "session", "version": 3, "id": "sess-" + name,
					"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": second},
				user(now.Add(-30*time.Second), nil),
				assistant(now.Add(-time.Second), "stop", nil),
			})
	}

	sessions := f.sessions()

	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want one card per working directory", sessions)
	}
	byDir := map[string]agent.Session{}
	for _, s := range sessions {
		byDir[s.Dir] = s
	}
	if s := byDir[first]; s.ID != "omp:sess-main" || s.Kind != agent.KindOmp {
		t.Errorf("website card = %+v, want the default agent dir's session", s)
	}
	// Both profiles keep a session for payments-api; the newest file decides.
	if s := byDir[second]; s.ID != "omp:sess-personal" {
		t.Errorf("payments-api card = %+v, want the newest profile's session", s)
	}
}

// A session run under the OS temp root is named temp-relative.
func TestTempRootSessions(t *testing.T) {
	f := newFixture(t)
	tempRoot := f.source.TempRoot
	dir := tempRoot + "/scratch/app"
	f.addProcess(32, dir, now.Add(-time.Hour))
	f.addSession(dir, "sess-tmp", now.Add(-time.Second),
		user(now.Add(-30*time.Second), nil),
		assistant(now.Add(-time.Second), "stop", nil),
	)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "omp:sess-tmp" || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want the temp-root session", sessions)
	}
}

// A file present in both pi's and omp's trees is two independent cards, one
// per kind, each read by its own detector.
func TestSameFileInBothTrees(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api" // outside home and temp root: both encode it the same way
	f.addProcess(40, dir, now.Add(-time.Hour))
	entries := []map[string]any{
		{"type": "session", "version": 3, "id": "sess-shared",
			"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": dir},
		user(now.Add(-30*time.Second), nil),
		assistant(now.Add(-time.Second), "toolUse", nil),
	}
	f.writeFile(f.source.SessionsRoots[0], "--work-payments-api--", "2026-11-20T10-00-00-000Z_sess-shared.jsonl",
		now.Add(-time.Minute), entries)
	piSource := pi.NewSource(f.home, f.procs)
	f.writeFile(piSource.SessionsDir, "--work-payments-api--", "2026-11-20T10-00-00-000Z_sess-shared.jsonl",
		now.Add(-time.Minute), entries)

	ompSessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	piSessions, err := piSource.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	sessions := append(ompSessions, piSessions...)

	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want one card per kind", sessions)
	}
	kinds := map[agent.Kind]bool{}
	for _, s := range sessions {
		kinds[s.Kind] = true
		if s.ID != string(s.Kind)+":sess-shared" {
			t.Errorf("id = %s, want %s:sess-shared", s.ID, s.Kind)
		}
	}
	if !kinds[agent.KindPi] || !kinds[agent.KindOmp] {
		t.Errorf("kinds = %v, want pi and omp", kinds)
	}
}

func TestSessionFolderNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the encoding is exercised with POSIX-shaped paths; Windows resolves them differently")
	}
	tests := []struct {
		name, cwd, home, tempRoot string
		want                      []string
	}{
		{
			name: "a directory under home is named home-relative",
			cwd:  "/Users/al/work/payments-api", home: "/Users/al", tempRoot: "/var/folders/T",
			want: []string{"-work-payments-api", "--Users-al-work-payments-api--"},
		},
		{
			name: "home itself is a bare dash",
			cwd:  "/Users/al", home: "/Users/al", tempRoot: "/var/folders/T",
			want: []string{"-", "--Users-al--"},
		},
		{
			name: "a directory under the temp root is checked before home",
			cwd:  "/var/folders/T/scratch/app", home: "/Users/al", tempRoot: "/var/folders/T",
			want: []string{"-tmp-scratch-app", "--var-folders-T-scratch-app--"},
		},
		{
			name: "a directory outside both keeps pi's absolute name",
			cwd:  "/work/payments-api", home: "/Users/al", tempRoot: "/var/folders/T",
			want: []string{"--work-payments-api--"},
		},
		{
			name: "punctuation survives as dashes",
			cwd:  "/Users/al/my:app/v2", home: "/Users/al", tempRoot: "/var/folders/T",
			want: []string{"-my-app-v2", "--Users-al-my-app-v2--"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionFolders(tt.cwd, tt.home, tt.tempRoot)
			if len(got) != len(tt.want) {
				t.Fatalf("sessionFolders(%q) = %v, want %v", tt.cwd, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sessionFolders(%q)[%d] = %q, want %q", tt.cwd, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// A session written before omp moved its folders sits under pi's absolute
// name; it is still found, and still tied to its folder by the header.
func TestLegacyFolderIsStillACandidate(t *testing.T) {
	f := newFixture(t)
	dir := f.home + "/work/payments-api"
	f.addProcess(50, dir, now.Add(-time.Hour))
	f.writeFile(f.source.SessionsRoots[0], encodedAbsolute(dir),
		"2026-11-20T10-00-00-000Z_sess-old.jsonl", now.Add(-time.Minute), []map[string]any{
			{"type": "session", "version": 3, "id": "sess-old",
				"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": dir},
			user(now.Add(-30*time.Second), nil),
			assistant(now.Add(-time.Second), "stop", nil),
		})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "omp:sess-old" || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want the legacy folder's session", sessions)
	}
}

// On macOS omp writes the /private prefix of the real temp path out of the
// session header; the header is matched under either spelling.
func TestHeaderSpellingAlias(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the alias is omp's macOS /private handling")
	}
	f := newFixture(t)
	real := "/private/tmp/scratch/app"
	f.addProcess(51, real, now.Add(-time.Hour))
	f.writeFile(f.source.SessionsRoots[0], f.canonicalFolder(real), "2026-11-20T10-00-00-000Z_sess-alias.jsonl",
		now.Add(-time.Minute), []map[string]any{
			{"type": "session", "version": 3, "id": "sess-alias",
				"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": "/tmp/scratch/app"},
			user(now.Add(-30*time.Second), nil),
			assistant(now.Add(-time.Second), "stop", nil),
		})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "omp:sess-alias" {
		t.Errorf("sessions = %+v, want the aliased session matched", sessions)
	}
}

func TestSessionsDirectoryResolution(t *testing.T) {
	home := t.TempDir()
	customAgent := filepath.Join(t.TempDir(), "omp-agent")
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "omp"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		configEnv     string
		agentEnv      string
		xdgEnv        string
		want          string
		skipOnWindows bool
	}{
		{
			name: "default",
			want: filepath.Join(home, ".omp", "agent", "sessions"),
		},
		{
			name:      "PI_CONFIG_DIR names the config root under home",
			configEnv: "custom-omp",
			want:      filepath.Join(home, "custom-omp", "agent", "sessions"),
		},
		{
			name:     "PI_CODING_AGENT_DIR moves the agent dir",
			agentEnv: customAgent,
			want:     filepath.Join(customAgent, "sessions"),
		},
		{
			name:          "an XDG_DATA_HOME with an omp folder takes over",
			xdgEnv:        xdg,
			want:          filepath.Join(xdg, "omp", "sessions"),
			skipOnWindows: true,
		},
		{
			name:          "an XDG_DATA_HOME without an omp folder changes nothing",
			xdgEnv:        filepath.Join(xdg, "unused"),
			want:          filepath.Join(home, ".omp", "agent", "sessions"),
			skipOnWindows: true,
		},
		{
			name:          "an agent-dir override pins the agent dir even under XDG",
			agentEnv:      customAgent,
			xdgEnv:        xdg,
			want:          filepath.Join(customAgent, "sessions"),
			skipOnWindows: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipOnWindows && runtime.GOOS == "windows" {
				t.Skip("omp ignores XDG on Windows")
			}
			t.Setenv("PI_CONFIG_DIR", tt.configEnv)
			t.Setenv("PI_CODING_AGENT_DIR", tt.agentEnv)
			t.Setenv("XDG_DATA_HOME", tt.xdgEnv)
			got := NewSource(home, &fakeProcesses{}).SessionsRoots[0]
			if got != tt.want {
				t.Errorf("sessions root = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProfilesAreResolvedFromDisk(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".omp")
	for _, name := range []string{"work", "personal"} {
		if err := os.MkdirAll(filepath.Join(root, "profiles", name, "agent", "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "profiles", "notes.txt"), []byte("not a profile"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	source := NewSource(home, &fakeProcesses{})

	want := []string{
		filepath.Join(root, "agent", "sessions"),
		filepath.Join(root, "profiles", "personal", "agent", "sessions"), // ReadDir order: alphabetical
		filepath.Join(root, "profiles", "work", "agent", "sessions"),
	}
	if len(source.SessionsRoots) != len(want) {
		t.Fatalf("roots = %v, want %v", source.SessionsRoots, want)
	}
	for i := range want {
		if source.SessionsRoots[i] != want[i] {
			t.Errorf("root[%d] = %q, want %q", i, source.SessionsRoots[i], want[i])
		}
	}
}

// A profile whose XDG folder omp has migrated to gets its sessions root there.
func TestProfileXDGRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("omp ignores XDG on Windows")
	}
	home := t.TempDir()
	xdg := t.TempDir()
	root := filepath.Join(home, ".omp")
	if err := os.MkdirAll(filepath.Join(root, "profiles", "work", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(xdg, "omp", "profiles", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("XDG_DATA_HOME", xdg)
	source := NewSource(home, &fakeProcesses{})

	want := filepath.Join(xdg, "omp", "profiles", "work", "sessions")
	if len(source.SessionsRoots) != 2 || source.SessionsRoots[1] != want {
		t.Errorf("roots = %v, want the work profile's sessions under %q", source.SessionsRoots, want)
	}
}

func TestMissingSessionsDirIsNotAnError(t *testing.T) {
	f := newFixture(t)
	f.addProcess(60, f.home+"/work/website", now.Add(-time.Hour))
	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}
	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card", sessions)
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-4a1f"
	f := newFixture(t)
	dir := f.home + "/work/payments-api"
	f.addProcess(61, dir, now.Add(-time.Hour))
	slot := map[string]any{"type": "title", "v": 1, "title": canary, "updatedAt": now.Add(-time.Hour).Format(time.RFC3339Nano),
		"pad": strings.Repeat(" ", 100)}
	header := map[string]any{"type": "session", "version": 3, "id": "sess-private",
		"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": dir}
	f.addRawSession(dir, "private", now.Add(-time.Second), slot, header,
		user(now.Add(-time.Minute), map[string]any{"content": canary}),
		assistant(now.Add(-40*time.Second), "toolUse", map[string]any{"content": canary}),
		map[string]any{"type": "message", "timestamp": now.Add(-35 * time.Second).Format(time.RFC3339Nano),
			"message": map[string]any{"role": "toolResult", "output": canary, "title": canary}},
		map[string]any{"type": "compaction", "timestamp": now.Add(-30 * time.Second).Format(time.RFC3339Nano),
			"summary": canary},
		map[string]any{"type": "custom_message", "timestamp": now.Add(-25 * time.Second).Format(time.RFC3339Nano),
			"content": canary},
		map[string]any{"type": "branch_summary", "timestamp": now.Add(-22 * time.Second).Format(time.RFC3339Nano),
			"summary": canary},
		map[string]any{"type": "model_usage", "timestamp": now.Add(-21 * time.Second).Format(time.RFC3339Nano),
			"purpose": canary, "usage": map[string]any{"totalTokens": 1234}},
		assistant(now.Add(-20*time.Second), "error",
			map[string]any{"content": canary, "usage": map[string]any{"totalTokens": 5678}}),
	)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusError || sessions[0].Context == nil {
		t.Fatalf("sessions = %+v, want the session read with its error and context", sessions)
	}
	if sessions[0].Context.Used != 5678 {
		t.Errorf("context = %+v, want 5678 from the newest message usage (model_usage is not the turn's)", sessions[0].Context)
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
