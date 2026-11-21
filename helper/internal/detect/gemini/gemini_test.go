package gemini

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
	if name != "gemini" {
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
	home := t.TempDir()
	procs := &fakeProcesses{}
	return &fixture{t: t, home: home, procs: procs, source: NewSource(home, procs)}
}

// addProcess registers a running gemini process for dir.
func (f *fixture) addProcess(pid int, dir string, started time.Time) {
	*f.procs = append(*f.procs, process.Info{PID: pid, Name: "gemini", Dir: dir, StartedAt: started})
}

// mapProject makes the registry point dir at slug.
func (f *fixture) mapProject(dir, slug string) {
	f.t.Helper()
	writeJSON(f.t, filepath.Join(f.source.Root, "projects.json"),
		map[string]any{"projects": map[string]string{dir: slug}})
}

// addMarker writes a .project_root marker claiming dir for slug.
func (f *fixture) addMarker(slug, dir string) {
	f.t.Helper()
	writeFile(f.t, filepath.Join(f.source.Root, "tmp", slug, ".project_root"), "\""+dir+"\"\n")
}

// addSession writes a session file for the project slug, whose records are the
// fields given, and whose last write is at written.
func (f *fixture) addSession(slug, id string, written time.Time, records ...map[string]any) {
	f.t.Helper()
	var lines []string
	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			f.t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	path := filepath.Join(f.source.Root, "tmp", slug, "chats", "session-20261119T110000-"+id+".jsonl")
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
	writeMtime(f.t, path, written)
}

// addHead is the metadata record Gemini CLI writes first.
func (f *fixture) addHead(id string, startTime time.Time) map[string]any {
	return map[string]any{
		"sessionId": id, "startTime": startTime.Format(time.RFC3339Nano),
		"messages": []any{}, "kind": "main",
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
		name    string
		started time.Time
		records []map[string]any
		want    agent.Status
		wantAge time.Duration
	}{
		{
			name:    "a prompt without its reply is working",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-30 * time.Second)},
			},
			want:    agent.StatusWorking,
			wantAge: 30 * time.Second,
		},
		{
			name:    "a finished reply is waiting",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "gemini", "timestamp": now.Add(-20 * time.Second)},
			},
			want:    agent.StatusWaiting,
			wantAge: 20 * time.Second,
		},
		{
			name:    "a reply with executing tool calls is working",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "gemini", "timestamp": now.Add(-20 * time.Second),
					"toolCalls": []any{map[string]any{"name": "run_shell_command", "status": "executing"}}},
			},
			want: agent.StatusWorking,
		},
		{
			name:    "a reply awaiting approval is waiting",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "gemini", "timestamp": now.Add(-20 * time.Second),
					"toolCalls": []any{map[string]any{"name": "run_shell_command", "status": "awaiting_approval"}}},
			},
			want: agent.StatusWaiting,
		},
		{
			name:    "an error record is error",
			started: now.Add(-time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-time.Minute)},
				{"type": "error", "timestamp": now.Add(-20 * time.Second)},
			},
			want: agent.StatusError,
		},
		{
			name:    "a finished reply untouched for hours is idle",
			started: now.Add(-4 * time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-3 * time.Hour)},
				{"type": "gemini", "timestamp": now.Add(-3 * time.Hour)},
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
			name:    "a prompt without its reply that hung for hours is idle",
			started: now.Add(-4 * time.Hour),
			records: []map[string]any{
				{"type": "user", "timestamp": now.Add(-3 * time.Hour)},
			},
			want: agent.StatusIdle,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			dir := "/work/payments-api"
			f.mapProject(dir, "payments-api")
			f.addProcess(100+i, dir, tt.started)
			f.addSession("payments-api", "abc", now.Add(-time.Second),
				append([]map[string]any{f.addHead("sess-abc", tt.started)}, tt.records...)...)

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
	f.mapProject(dir, "agent-lights")
	f.addProcess(4242, dir, now.Add(-time.Hour))
	f.addSession("agent-lights", "abc12345", now.Add(-time.Second),
		f.addHead("sess-uuid-1", started),
		map[string]any{"type": "user", "timestamp": now.Add(-42 * time.Second)},
	)

	s := f.sessions()[0]

	if s.ID != "gemini:sess-uuid-1" || s.Kind != agent.KindGemini || s.Dir != dir {
		t.Errorf("unexpected identity: %+v", s)
	}
	if !s.StartedAt.Equal(started) {
		t.Errorf("startedAt = %v, want %v (the session file's start)", s.StartedAt, started)
	}
	if got := s.Age(now); got != 42*time.Second {
		t.Errorf("age = %v, want 42s", got)
	}
}

func TestProjectMapping(t *testing.T) {
	f := newFixture(t)
	dir := "/work/website"
	f.addProcess(7, dir, now.Add(-time.Hour))
	f.addSession("website", "abc", now.Add(-time.Minute), f.addHead("sess-1", now.Add(-time.Hour)))
	f.mapProject(dir, "website") // the registry claims the sessions

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "gemini:sess-1" {
		t.Fatalf("sessions = %+v, want the website session", sessions)
	}

	f = newFixture(t) // no registry: the marker claims them
	f.addProcess(8, dir, now.Add(-time.Hour))
	f.addSession("website", "abc", now.Add(-time.Minute), f.addHead("sess-2", now.Add(-time.Hour)))
	f.addMarker("website", dir)

	sessions = f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "gemini:sess-2" {
		t.Errorf("sessions = %+v, want the website session via the marker", sessions)
	}
}

func TestNoProjectMappingIsWaitingCard(t *testing.T) {
	f := newFixture(t)
	f.addProcess(7, "/work/website", now.Add(-time.Hour))
	f.addSession("website", "abc", now.Add(-time.Minute), f.addHead("sess-1", now.Add(-time.Hour)))
	// neither the registry nor a marker claims the sessions

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "gemini:7" || sessions[0].Status != agent.StatusWaiting {
		t.Errorf("sessions = %+v, want one waiting card for the process", sessions)
	}
}

func TestSubagentSessionsGetNoCard(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.mapProject(dir, "payments-api")
	f.addProcess(9, dir, now.Add(-time.Hour))
	f.addSession("payments-api", "main", now.Add(-time.Minute),
		f.addHead("sess-main", now.Add(-time.Hour)),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
		map[string]any{"type": "gemini", "timestamp": now.Add(-time.Minute)},
	)
	// A subagent session, nested one level down and recorded with kind subagent.
	subagentHead := f.addHead("sess-sub", now.Add(-50*time.Minute))
	subagentHead["kind"] = "subagent"
	writeJSONLines(f.t,
		filepath.Join(f.source.Root, "tmp", "payments-api", "chats", "sess-main", "session-20261119T111000-sub1.jsonl"),
		subagentHead, map[string]any{"type": "user", "timestamp": now.Add(-10 * time.Second)},
	)
	// A subagent file at the top level is skipped for its kind.
	f.addSession("payments-api", "top", now.Add(-5*time.Second), subagentHead,
		map[string]any{"type": "user", "timestamp": now.Add(-5 * time.Second)},
	)

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "gemini:sess-main" {
		t.Errorf("sessions = %+v, want only the main session", sessions)
	}
	if sessions[0].Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting: the subagent's prompt is not the session's", sessions[0].Status)
	}
}

func TestContextFromTail(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.mapProject(dir, "payments-api")
	f.addProcess(11, dir, now.Add(-time.Hour))
	f.addSession("payments-api", "abc", now.Add(-time.Second),
		f.addHead("sess-1", now.Add(-time.Hour)),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
		map[string]any{"type": "gemini", "timestamp": now.Add(-time.Minute),
			"tokens": map[string]any{"input": 38_000, "output": 4_000, "cached": 0, "total": 42_000}},
	)

	s := f.sessions()[0]

	if s.Context == nil || s.Context.Used != 42_000 || s.Context.Limit != 0 {
		t.Errorf("context = %+v, want 42000 used and no limit: Gemini CLI stores none", s.Context)
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
			f.mapProject(dir, "payments-api")
			f.addProcess(300+i, dir, now.Add(-time.Hour))
			f.addSession("payments-api", strconv.Itoa(i), now.Add(-time.Second),
				f.addHead("sess-"+strconv.Itoa(i), now.Add(-time.Hour)),
				map[string]any{"type": "gemini", "timestamp": now.Add(-time.Minute),
					"tokens": map[string]any{"total": 77_000}},
				map[string]any{"type": "gemini", "timestamp": now.Add(-30 * time.Second),
					"toolCalls": []any{map[string]any{"result": strings.Repeat("x", tt.resultSize)}}},
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

func TestSkipsExitedProcesses(t *testing.T) {
	f := newFixture(t)
	dir := "/work/payments-api"
	f.mapProject(dir, "payments-api")
	f.addSession("payments-api", "abc", now.Add(-time.Minute),
		f.addHead("sess-1", now.Add(-time.Hour)),
		map[string]any{"type": "user", "timestamp": now.Add(-2 * time.Minute)},
	) // no process: the session's run has exited
	f.addProcess(3, "", now.Add(-time.Hour)) // no working directory: not a session

	if sessions := f.sessions(); len(sessions) != 0 {
		t.Errorf("sessions = %+v, want none", sessions)
	}
}

func TestConversationContentNeverLeaks(t *testing.T) {
	const canary = "CANARY-6b2e"
	f := newFixture(t)
	dir := "/work/payments-api"
	f.mapProject(dir, "payments-api")
	f.addProcess(13, dir, now.Add(-time.Hour))
	head := f.addHead("sess-private", now.Add(-time.Hour))
	head["summary"] = canary
	f.addSession("payments-api", "private", now.Add(-time.Second),
		head,
		map[string]any{"type": "user", "timestamp": now.Add(-time.Minute), "content": canary},
		map[string]any{"type": "gemini", "timestamp": now.Add(-40 * time.Second),
			"content": canary, "displayContent": canary, "thoughts": canary,
			"tokens": map[string]any{"total": 1234}},
		map[string]any{"type": "gemini", "timestamp": now.Add(-35 * time.Second),
			"toolCalls": []any{map[string]any{"name": "read_file", "args": canary, "result": canary,
				"displayName": canary}}},
		map[string]any{"$set": map[string]any{"summary": canary}},
		map[string]any{"type": "error", "timestamp": now.Add(-20 * time.Second)},
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

func TestMissingGeminiDirIsNotAnError(t *testing.T) {
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

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
}

func writeJSONLines(t *testing.T, path string, values ...map[string]any) {
	t.Helper()
	var lines []string
	for _, v := range values {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
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
