package qwen

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
	if name != "qwen" {
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
	t.Setenv("QWEN_HOME", "") // the developer's own setting must not leak in
	home := t.TempDir()
	procs := &fakeProcesses{}
	return &fixture{t: t, home: home, procs: procs, source: NewSource(home, procs)}
}

// addProcess registers a running qwen process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	*f.procs = append(*f.procs, process.Info{PID: pid, Name: "qwen", Dir: dir, StartedAt: started})
}

// addSession writes a session file for dir whose records are the fields given
// (each record carries the session id and the working directory, as Qwen Code
// writes them), and whose last write is at written.
func (f *fixture) addSession(dir, id string, written time.Time, records ...map[string]any) {
	f.t.Helper()
	var lines []string
	for _, r := range records {
		if _, ok := r["sessionId"]; !ok {
			r["sessionId"] = id
		}
		if _, ok := r["cwd"]; !ok {
			r["cwd"] = dir
		}
		data, err := json.Marshal(r)
		if err != nil {
			f.t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	path := filepath.Join(f.source.Root, "projects", sanitizeCwd(dir), "chats", id+".jsonl")
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
	writeMtime(f.t, path, written)
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
	recent := func(at time.Time) time.Time { return at }
	tests := []struct {
		name    string
		started time.Time
		records []map[string]any
		want    agent.Status
		wantAge time.Duration
	}{
		{
			name:    "a prompt without its turn is working",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": recent(now.Add(-30 * time.Second))},
			},
			want:    agent.StatusWorking,
			wantAge: 30 * time.Second,
		},
		{
			name:    "a completed turn is waiting",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "assistant", "timestamp": now.Add(-40 * time.Second)},
				{"type": "system", "subtype": "turn_result", "state": "completed", "endedAt": now.Add(-20 * time.Second)},
			},
			want:    agent.StatusWaiting,
			wantAge: 20 * time.Second,
		},
		{
			name:    "a turn that ended in an error is error",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "system", "subtype": "turn_result", "state": "error", "endedAt": now.Add(-20 * time.Second)},
			},
			want: agent.StatusError,
		},
		{
			name:    "a cancelled turn is waiting, not error",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "system", "subtype": "turn_result", "state": "cancelled", "endedAt": now.Add(-20 * time.Second)},
			},
			want: agent.StatusWaiting,
		},
		{
			name:    "a finished turn untouched for hours is idle",
			started: now.Add(-4 * time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-3 * time.Hour)},
				{"type": "system", "subtype": "turn_result", "state": "completed", "endedAt": now.Add(-3 * time.Hour)},
			},
			want: agent.StatusIdle,
		},
		{
			name:    "a prompt from a previous run is not working",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-3 * time.Hour)},
			},
			want: agent.StatusWaiting,
		},
		{
			name:    "a prompt without its turn that hung for hours is idle",
			started: now.Add(-4 * time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-3 * time.Hour)},
			},
			want: agent.StatusIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := "/work/payments-api"
			f.addProcess(100, dir, tt.started)
			f.addSession(dir, "session-a", now.Add(-time.Second), tt.records...)

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
	started := now.Add(-time.Hour)
	f.addProcess(4242, dir, started)
	f.addSession(dir, "session-abc", now.Add(-time.Second),
		map[string]any{"type": "system", "timestamp": started},
		map[string]any{"type": "user", "timestamp": now.Add(-42 * time.Second)},
	)

	s := f.sessions()[0]

	if s.ID != "qwen:session-abc" || s.Kind != agent.KindQwen || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(started) {
		t.Errorf("startedAt = %v, want %v (the session file's start)", s.StartedAt, started)
	}
	if got := s.Age(now); got != 42*time.Second {
		t.Errorf("age = %v, want 42s", got)
	}
}

func TestNoSessionFileYetIsWaitingCard(t *testing.T) {
	f := newFixture(t)
	started := now.Add(-time.Hour)
	f.addProcess(7, "/work/website", started)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "qwen:7" || sessions[0].Status != agent.StatusWaiting {
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
	f.addSession(dir, "session-a", now.Add(-time.Minute),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
	)
	f.addSession("/work/exited", "session-c", now.Add(-time.Minute),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
	) // no process: the session's run has exited
	f.addProcess(3, "", now.Add(-time.Hour)) // no working directory: not a session
	other := "/work/other (v2)"
	f.addProcess(4, other, now.Add(-time.Hour))
	f.addSession(other, "session-b", now.Add(-time.Minute),
		map[string]any{"cwd": "/work/other", "type": "user", "timestamp": now.Add(-2 * time.Minute)},
	) // records carry another folder's cwd: a waiting card for the process

	sessions := f.sessions()

	if len(sessions) != 2 {
		t.Fatalf("sessions = %+v, want the payments-api card and the waiting card of other (v2)", sessions)
	}
	if sessions[0].ID != "qwen:session-a" || sessions[0].Dir != dir {
		t.Errorf("first card = %+v, want payments-api's session", sessions[0])
	}
	if sessions[1].ID != "qwen:4" || sessions[1].Status != agent.StatusWaiting {
		t.Errorf("second card = %+v, want a waiting card for pid 4", sessions[1])
	}
}

func TestNewestSessionFileIsTheActiveOne(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(9, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-old", now.Add(-3*time.Hour),
		map[string]any{"type": "user", "timestamp": now.Add(-3 * time.Hour)},
		map[string]any{"type": "system", "subtype": "turn_result", "state": "error", "endedAt": now.Add(-3 * time.Hour)},
	)
	f.addSession(dir, "session-new", now.Add(-time.Minute),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
		map[string]any{"type": "system", "subtype": "turn_result", "state": "completed", "endedAt": now.Add(-time.Minute)},
	)

	s := f.sessions()[0]

	if s.ID != "qwen:session-new" {
		t.Errorf("id = %s, want the newest session file", s.ID)
	}
	if s.Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting from the newest file's completed turn", s.Status)
	}
	if s.Age(now) != time.Minute {
		t.Errorf("age = %v, want 1m", s.Age(now))
	}
}

func TestContextFromTail(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(11, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-a", now.Add(-time.Second),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
		map[string]any{"type": "assistant", "timestamp": now.Add(-time.Minute),
			"usageMetadata":     map[string]any{"promptTokenCount": 40_000, "candidatesTokenCount": 2_000, "totalTokenCount": 42_000},
			"contextWindowSize": 131_072},
		map[string]any{"type": "system", "subtype": "turn_result", "state": "completed", "endedAt": now.Add(-20 * time.Second)},
	)

	s := f.sessions()[0]

	if s.Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting", s.Status)
	}
	if s.Context == nil || s.Context.Used != 42_000 || s.Context.Limit != 131_072 {
		t.Errorf("context = %+v, want 42000 of 131072", s.Context)
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
			f.addSession(dir, "session-"+strconv.Itoa(i), now.Add(-time.Second),
				map[string]any{"type": "assistant", "timestamp": now.Add(-time.Minute),
					"usageMetadata": map[string]any{"totalTokenCount": 77_000}},
				map[string]any{"type": "tool_result", "timestamp": now.Add(-30 * time.Second), "toolCallResult": strings.Repeat("x", tt.resultSize)},
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

func TestSubagentTrafficIsNotTheSession(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(12, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-a", now.Add(-time.Second),
		map[string]any{"type": "user", "timestamp": now.Add(-3 * time.Minute)},
		map[string]any{"type": "system", "subtype": "turn_result", "state": "completed", "endedAt": now.Add(-2 * time.Minute)},
		map[string]any{"type": "user", "isSidechain": true, "timestamp": now.Add(-time.Minute)},
	)

	s := f.sessions()[0]

	if s.Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting: the subagent's prompt is not the session's turn", s.Status)
	}
	if s.Age(now) != 2*time.Minute {
		t.Errorf("age = %v, want 2m since the main turn ended", s.Age(now))
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-9d4c"
	f := newFixture(t)
	dir := "/work/payments-api"
	f.addProcess(13, dir, now.Add(-time.Hour))
	f.addSession(dir, "session-private", now.Add(-time.Second),
		map[string]any{"type": "user", "timestamp": now.Add(-time.Minute),
			"message": map[string]any{"role": "user", "content": canary}},
		map[string]any{"type": "assistant", "timestamp": now.Add(-40 * time.Second),
			"message":           map[string]any{"role": "assistant", "content": canary},
			"usageMetadata":     map[string]any{"totalTokenCount": 1234},
			"contextWindowSize": 200_000},
		map[string]any{"type": "tool_result", "timestamp": now.Add(-35 * time.Second),
			"toolCallResult": map[string]any{"output": canary}},
		map[string]any{"type": "system", "subtype": "turn_result", "state": "error",
			"endedAt": now.Add(-20 * time.Second), "promptText": canary, "resultText": canary},
	)
	writeFile(f.t, filepath.Join(f.source.Root, "projects", sanitizeCwd(dir), "chats", "session-private.title.json"),
		`{"title":"`+canary+`"}`)

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

func TestSanitizeCwd(t *testing.T) {
	tests := []struct{ dir, want string }{
		{"/Users/me/my app", "-Users-me-my-app"},
		{"/work/payments-api", "-work-payments-api"},
		{"/", "-"},
	}
	for _, tt := range tests {
		if got := sanitizeCwd(tt.dir); got != tt.want {
			t.Errorf("sanitizeCwd(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestHomeOverride(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "qwen-home")
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"default", "", filepath.Join(home, ".qwen")},
		{"QWEN_HOME", custom, custom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QWEN_HOME", tt.env)
			if got := NewSource(home, &fakeProcesses{}).Root; got != tt.want {
				t.Errorf("root = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingProjectsDirIsNotAnError(t *testing.T) {
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
