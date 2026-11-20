package amp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/protocol"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t          *testing.T
	home       string
	threadsDir string
	source     *Source
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", "") // the developer's own setting must not leak in
	home := t.TempDir()
	source := NewSource(home)
	return &fixture{t: t, home: home, threadsDir: source.ThreadsDir, source: source}
}

// addThread writes a thread mirror whose fields are the ones given, with the
// id, the working directory and the creation time filled in, and whose last
// write is at written.
func (f *fixture) addThread(id, dir string, written time.Time, fields map[string]any) {
	f.t.Helper()
	if fields == nil {
		fields = map[string]any{}
	}
	if _, ok := fields["id"]; !ok {
		fields["id"] = id
	}
	if _, ok := fields["created"]; !ok {
		fields["created"] = written.UnixMilli()
	}
	if dir != "" {
		if _, ok := fields["env"]; !ok {
			fields["env"] = map[string]any{"initial": map[string]any{"cwd": dir}}
		}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		f.t.Fatal(err)
	}
	path := filepath.Join(f.threadsDir, id+".json")
	writeFile(f.t, path, string(data))
	writeMtime(f.t, path, written)
}

// message builds a message mirror: role, its content blocks and its state.
func msg(role string, state map[string]any, content ...map[string]any) map[string]any {
	m := map[string]any{"role": role, "messageId": 0, "content": content}
	if state != nil {
		m["state"] = state
	}
	return m
}

func state(kind, stopReason string) map[string]any {
	s := map[string]any{"type": kind}
	if stopReason != "" {
		s["stopReason"] = stopReason
	}
	return s
}

// run is a tool result block with the given run status; result is never read.
func run(status string) map[string]any {
	return map[string]any{"type": "tool_result", "toolUseID": "toolu_1", "run": map[string]any{"status": status}}
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
		message map[string]any
		want    agent.Status
	}{
		{
			name:    "a message still streaming is working",
			message: msg("assistant", state("streaming", ""), map[string]any{"type": "text"}),
			want:    agent.StatusWorking,
		},
		{
			name:    "a tool result queued is working",
			message: msg("user", nil, run("queued")),
			want:    agent.StatusWorking,
		},
		{
			name:    "a tool result in progress is working",
			message: msg("user", nil, run("in-progress")),
			want:    agent.StatusWorking,
		},
		{
			name:    "a tool result blocked on the user is waiting",
			message: msg("user", nil, run("blocked-on-user")),
			want:    agent.StatusWaiting,
		},
		{
			name:    "a tool result refused by the user is waiting",
			message: msg("user", nil, run("rejected-by-user")),
			want:    agent.StatusWaiting,
		},
		{
			name:    "a failed tool result is error",
			message: msg("user", nil, run("error")),
			want:    agent.StatusError,
		},
		{
			name:    "a tool result cancelled is a turn that is over, waiting",
			message: msg("user", state("complete", "tool_use"), run("cancelled")),
			want:    agent.StatusWaiting,
		},
		{
			name:    "a complete message stopped for good is waiting",
			message: msg("assistant", state("complete", "end_turn"), map[string]any{"type": "text"}),
			want:    agent.StatusWaiting,
		},
		{
			name:    "a complete message that stopped on an error is error",
			message: msg("assistant", state("complete", "error"), map[string]any{"type": "text"}),
			want:    agent.StatusError,
		},
		{
			name:    "a settled tool result is a finished turn, waiting",
			message: msg("user", state("complete", "tool_use"), run("done")),
			want:    agent.StatusWaiting,
		},
		{
			name:    "a thread without messages is waiting",
			message: nil,
			want:    agent.StatusWaiting,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := f.project("payments-api")
			var messages []map[string]any
			if tt.message != nil {
				messages = append(messages, tt.message)
			}
			f.addThread("T-1111", dir, now.Add(-time.Second),
				map[string]any{"messages": messages})

			sessions := f.sessions()

			if len(sessions) != 1 {
				t.Fatalf("got %d sessions, want 1", len(sessions))
			}
			if sessions[0].Status != tt.want {
				t.Errorf("status = %s, want %s", sessions[0].Status, tt.want)
			}
		})
	}
}

// project makes a working directory under the temporary home.
func (f *fixture) project(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.home, "work", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

func TestSessionFields(t *testing.T) {
	f := newFixture(t)
	dir := f.project("agent-lights")
	created := now.Add(-50 * time.Minute)
	assistant := msg("assistant", state("complete", "end_turn"), map[string]any{"type": "text"})
	assistant["usage"] = map[string]any{
		"inputTokens": 100, "cacheCreationInputTokens": 10, "cacheReadInputTokens": 5,
		"totalInputTokens": 115,
	}
	f.addThread("T-ab12", dir, now.Add(-time.Second),
		map[string]any{
			"created": created.UnixMilli(),
			"messages": []map[string]any{
				msg("user", nil, map[string]any{"type": "text"}),
				assistant,
			},
		})

	s := f.sessions()[0]

	if s.ID != "amp:T-ab12" || s.Kind != agent.KindAmp || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(created) {
		t.Errorf("startedAt = %v, want the thread's creation %v", s.StartedAt, created)
	}
	if s.Context == nil || s.Context.Used != 115 || s.Context.Limit != 0 {
		t.Errorf("context = %+v, want 115 used, no limit (amp stores no window size)", s.Context)
	}
	if got := s.Age(now); got != time.Second {
		t.Errorf("age = %v, want 1s, the mirror's last write", got)
	}
}

func TestQuietThreadIsNotShown(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addThread("T-old", dir, now.Add(-10*time.Minute),
		map[string]any{"messages": []map[string]any{
			msg("assistant", state("complete", "end_turn"), map[string]any{"type": "text"}),
		}})

	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("sessions = %+v, want none: amp wrote nothing for 10 minutes", sessions)
	}
}

func TestRunningThreadKeptWhateverItsWriteAge(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	running := map[string]any{"messages": []map[string]any{
		msg("user", nil, run("in-progress")),
	}}
	f.addThread("T-running", dir, now.Add(-10*time.Minute), running) // a long silent tool run

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWorking {
		t.Fatalf("sessions = %+v, want the running thread, working", sessions)
	}
	// But a run silent for 2 h is one that died mid-turn.
	f.addThread("T-dead", dir, now.Add(-3*time.Hour), running)
	sessions = f.sessions()
	if len(sessions) != 1 {
		t.Errorf("sessions = %+v, want only the young running thread", sessions)
	}
}

func TestBlockedThreadKeepsWaiting(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addThread("T-blocked", dir, now.Add(-3*time.Hour),
		map[string]any{"messages": []map[string]any{
			msg("user", nil, run("blocked-on-user")),
		}})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWaiting {
		t.Fatalf("sessions = %+v, want the blocked thread, waiting for as long as it takes", sessions)
	}
	if got := sessions[0].Age(now); got != 3*time.Hour {
		t.Errorf("age = %v, want 3h, the last write", got)
	}
}

func TestThreadWithoutWorkingDirectoryGetsNoCard(t *testing.T) {
	f := newFixture(t)
	f.addThread("T-noenv", "", now.Add(-time.Second),
		map[string]any{"env": map[string]any{"initial": map[string]any{"platform": map[string]any{"os": "darwin"}}},
			"messages": []map[string]any{msg("user", nil, map[string]any{"type": "text"})}})
	f.addThread("T-relative", "payments-api", now.Add(-time.Second), nil) // not an absolute path

	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("sessions = %+v, want none: the mirror names no working directory", sessions)
	}
}

func TestWorkingDirectoryCandidates(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	tests := []struct {
		name string
		env  map[string]any
	}{
		{"env.cwd", map[string]any{"cwd": dir}},
		{"env.initial.cwd", map[string]any{"initial": map[string]any{"cwd": dir}}},
		{"env.initial.pwd", map[string]any{"initial": map[string]any{"pwd": dir}}},
		{"env.initial.workingDir", map[string]any{"initial": map[string]any{"workingDir": dir}}},
		{"env.initial.workingDirectory", map[string]any{"initial": map[string]any{"workingDirectory": dir}}},
		{"env.initial.directory", map[string]any{"initial": map[string]any{"directory": dir}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.addThread("T-2222", "", now.Add(-time.Second),
				map[string]any{"env": tt.env,
					"messages": []map[string]any{msg("user", nil, map[string]any{"type": "text"})}})

			sessions := f.sessions()

			if len(sessions) != 1 || sessions[0].Dir != dir {
				t.Errorf("sessions = %+v, want the thread in %s", sessions, dir)
			}
		})
	}
}

func TestUnreadableMirrorIsAnErrorButOthersShow(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	writeFile(f.t, filepath.Join(f.threadsDir, "T-broken.json"), "{not json")
	f.addThread("T-good", dir, now.Add(-time.Second), nil)

	sessions, err := f.source.Sessions(context.Background(), now)
	if err == nil {
		t.Fatal("err = nil, want the broken mirror's error")
	}
	if len(sessions) != 1 || sessions[0].ID != "amp:T-good" {
		t.Errorf("sessions = %+v, want the readable thread's card despite the error", sessions)
	}
}

func TestMissingThreadsDirIsNotAnError(t *testing.T) {
	f := newFixture(t)
	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}
	if len(sessions) != 0 {
		t.Errorf("sessions = %+v, want none", sessions)
	}
}

func TestNonThreadFilesAreSkipped(t *testing.T) {
	f := newFixture(t)
	dir := f.project("payments-api")
	writeFile(f.t, filepath.Join(f.threadsDir, "notes.txt"), "not a thread")
	mkdir(t, filepath.Join(f.threadsDir, "T-dir.json"))
	f.addThread("T-ok", dir, now.Add(-time.Second), nil)

	sessions, err := f.source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "amp:T-ok" {
		t.Errorf("sessions = %+v, want only the thread", sessions)
	}
}

func TestDefaultThreadsDir(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(t.TempDir(), "xdg")
	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{"default", "", filepath.Join(home, ".local", "share", "amp", "threads")},
		{"XDG_DATA_HOME", xdg, filepath.Join(xdg, "amp", "threads")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", tt.xdg)
			if got := DefaultThreadsDir(home); got != tt.want {
				t.Errorf("threads dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-4e9f"
	f := newFixture(t)
	dir := f.project("payments-api")
	f.addThread("T-priv", dir, now.Add(-time.Second),
		map[string]any{
			"title": canary, // amp infers titles from the conversation
			"env": map[string]any{
				"other":     canary,
				"initial":   map[string]any{"platform": map[string]any{"os": canary}, "cwd": dir},
				"directory": canary, // a relative one: never trusted, never kept
			},
			"messages": []map[string]any{
				msg("user", nil,
					map[string]any{"type": "text", "text": canary},
					map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash",
						"input": map[string]any{"command": canary}}),
				msg("assistant", state("streaming", ""),
					map[string]any{"type": "thinking", "thinking": canary},
					map[string]any{"type": "redacted_thinking", "data": canary},
					map[string]any{"type": "tool_result", "toolUseID": "toolu_1",
						"run": map[string]any{"status": "in-progress", "result": canary, "error": canary}}),
			},
		})

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusWorking || sessions[0].Dir != dir {
		t.Fatalf("sessions = %+v, want the thread read from its mirror, working", sessions)
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
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

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
