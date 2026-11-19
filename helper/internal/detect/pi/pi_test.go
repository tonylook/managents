package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	if name != "pi" {
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
	t.Setenv("PI_CODING_AGENT_DIR", "") // the developer's own settings must not leak in
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	home := t.TempDir()
	procs := &fakeProcesses{}
	return &fixture{t: t, home: home, procs: procs, source: NewSource(home, procs)}
}

// addProcess registers a running pi process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	*f.procs = append(*f.procs, process.Info{PID: pid, Name: "pi", Dir: dir, StartedAt: started})
}

// addSession writes a session file for dir whose entries are the fields given
// (a session header first, unless the caller passes their own), and whose last
// write is at written.
func (f *fixture) addSession(dir, id string, written time.Time, entries ...map[string]any) {
	f.t.Helper()
	if len(entries) == 0 || entries[0]["type"] != "session" {
		header := map[string]any{
			"type": "session", "version": 3, "id": id,
			"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": dir,
		}
		entries = append([]map[string]any{header}, entries...)
	}
	var lines []string
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			f.t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	path := filepath.Join(f.source.SessionsDir, sessionDir(dir), "2026-11-19T11-00-00-000Z_"+id+".jsonl")
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
	writeMtime(f.t, path, written)
}

// user, assistant and toolResult are the message entries pi appends during a
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
			name:    "a pending reply is a turn in progress",
			started: now.Add(-time.Hour),
			entries: []map[string]any{
				user(now.Add(-time.Minute), nil),
				assistant(now.Add(-20*time.Second), "pending", nil),
			},
			want: agent.StatusWorking,
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
			dir := "/work/payments-api"
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
	dir := "/work/agent-lights"
	started := now.Add(-2 * time.Hour)
	f.addProcess(4242, dir, now.Add(-time.Hour))
	header := map[string]any{"type": "session", "version": 3, "id": "sess-uuid-1",
		"timestamp": started.Format(time.RFC3339Nano), "cwd": dir}
	f.addSession(dir, "ignored", now.Add(-time.Second), header,
		user(now.Add(-42*time.Second), nil),
		assistant(now.Add(-20*time.Second), "stop", nil),
	)

	s := f.sessions()[0]

	if s.ID != "pi:sess-uuid-1" || s.Kind != agent.KindPi || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(started) {
		t.Errorf("startedAt = %v, want %v (the session header's timestamp)", s.StartedAt, started)
	}
	if got := s.Age(now); got != 20*time.Second {
		t.Errorf("age = %v, want 20s since the turn ended", got)
	}
}

func TestNoSessionFileYetIsWaitingCard(t *testing.T) {
	f := newFixture(t)
	started := now.Add(-time.Hour)
	f.addProcess(7, "/work/website", started)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "pi:7" || sessions[0].Status != agent.StatusWaiting {
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
	f.addSession(dir, "sess-a", now.Add(-time.Minute),
		user(now.Add(-2*time.Minute), nil),
		assistant(now.Add(-time.Minute), "stop", nil),
	)
	f.addSession("/work/exited", "sess-b", now.Add(-time.Minute),
		user(now.Add(-2*time.Minute), nil),
	) // no process: the session's run has exited
	f.addProcess(3, "", now.Add(-time.Hour)) // no working directory: not a session
	other := "/work/other"
	f.addProcess(4, other, now.Add(-time.Hour))
	f.addSession(other, "sess-c", now.Add(-time.Minute),
		map[string]any{"type": "session", "version": 3, "id": "sess-x",
			"timestamp": now.Add(-time.Hour).Format(time.RFC3339Nano), "cwd": "/work/elsewhere"},
		user(now.Add(-2*time.Minute), nil),
	) // header carries another folder's cwd: a waiting card for the process

	sessions := f.sessions()

	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want the payments-api card and the waiting card of other", sessions)
	}
	if sessions[0].ID != "pi:sess-a" || sessions[0].Dir != dir {
		t.Errorf("first card = %+v, want payments-api's session", sessions[0])
	}
	if sessions[1].ID != "pi:4" || sessions[1].Status != agent.StatusWaiting {
		t.Errorf("second card = %+v, want a waiting card for pid 4", sessions[1])
	}
}

func TestNewestSessionFileIsTheActiveOne(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(9, dir, now.Add(-time.Hour))
	f.addSession(dir, "sess-old", now.Add(-3*time.Hour),
		user(now.Add(-3*time.Hour), nil),
		assistant(now.Add(-3*time.Hour), "error", nil),
	)
	f.addSession(dir, "sess-new", now.Add(-time.Minute),
		user(now.Add(-2*time.Minute), nil),
		assistant(now.Add(-time.Minute), "stop", nil),
	)

	s := f.sessions()[0]

	if s.ID != "pi:sess-new" {
		t.Errorf("id = %s, want the newest session file", s.ID)
	}
	if s.Status != agent.StatusWaiting || s.Age(now) != time.Minute {
		t.Errorf("session = %+v, want waiting since the newest file's finished turn", s)
	}
}

func TestContextFromTail(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(11, dir, now.Add(-time.Hour))
	f.addSession(dir, "sess-a", now.Add(-time.Second),
		user(now.Add(-2*time.Minute), nil),
		assistant(now.Add(-time.Minute), "stop",
			map[string]any{"usage": map[string]any{"input": 40_000, "output": 2_000, "cacheRead": 3_000, "totalTokens": 45_000}}),
	)

	s := f.sessions()[0]

	if s.Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting", s.Status)
	}
	if s.Context == nil || s.Context.Used != 45_000 || s.Context.Limit != 0 {
		t.Errorf("context = %+v, want 45000 used and no limit: pi stores none", s.Context)
	}
}

func TestContextBehindAHugeToolResult(t *testing.T) {
	tests := []struct {
		name       string
		resultSize int
		wantUsed   int // 0: no context
	}{
		{"larger than the first window", 3 * sessionTail, 77_000},
		{"larger than the largest window", maxSessionTail, 0},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := "/work/payments-api"
			f.addProcess(300+i, dir, now.Add(-time.Hour))
			f.addSession(dir, "sess-"+strconv.Itoa(i), now.Add(-time.Second),
				assistant(now.Add(-time.Minute), "stop",
					map[string]any{"usage": map[string]any{"totalTokens": 77_000}}),
				map[string]any{"type": "custom_message", "timestamp": now.Add(-30 * time.Second).Format(time.RFC3339Nano),
					"custom": strings.Repeat("x", tt.resultSize)},
			)

			s := f.sessions()[0]

			switch {
			case tt.wantUsed == 0 && s.Context != nil:
				t.Errorf("context = %+v, want none: the scan must stay bounded", s.Context)
			case tt.wantUsed != 0 && (s.Context == nil || s.Context.Used != tt.wantUsed):
				t.Errorf("context = %+v, want %d used", s.Context, tt.wantUsed)
			}
		})
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-2f8d"
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(13, dir, now.Add(-time.Hour))
	f.addSession(dir, "sess-private", now.Add(-time.Second),
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
		assistant(now.Add(-20*time.Second), "error",
			map[string]any{"content": canary, "usage": map[string]any{"totalTokens": 1234}}),
	)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].Status != agent.StatusError || sessions[0].Context == nil {
		t.Fatalf("sessions = %+v, want the session read with its error and context", sessions)
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

func TestSessionDirEncoding(t *testing.T) {
	tests := []struct{ dir, want string }{
		{"/Users/al/dev/app", "--Users-al-dev-app--"},
		{"/work/payments-api", "--work-payments-api--"},
		{"/", "----"},
	}
	for _, tt := range tests {
		if got := sessionDir(tt.dir); got != tt.want {
			t.Errorf("sessionDir(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestSessionDirectoryOverride(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "pi-config")
	sessionDir := filepath.Join(t.TempDir(), "pi-sessions")
	tests := []struct {
		name       string
		sessionEnv string
		configEnv  string
		want       string
	}{
		{"default", "", "", filepath.Join(home, ".pi", "agent", "sessions")},
		{"PI_CODING_AGENT_DIR", "", custom, filepath.Join(custom, "sessions")},
		{"PI_CODING_AGENT_SESSION_DIR", sessionDir, custom, sessionDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PI_CODING_AGENT_SESSION_DIR", tt.sessionEnv)
			t.Setenv("PI_CODING_AGENT_DIR", tt.configEnv)
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
