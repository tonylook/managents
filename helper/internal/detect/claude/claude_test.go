package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

var now = time.Date(2026, 10, 9, 16, 30, 0, 0, time.UTC)

// fakeProcesses maps pid -> start time.
type fakeProcesses map[int]time.Time

func (f fakeProcesses) StartTime(_ context.Context, pid int) (time.Time, bool) {
	t, ok := f[pid]
	return t, ok
}

func (f fakeProcesses) FindByName(context.Context, string) ([]process.Info, error) { return nil, nil }

type fixture struct {
	t      *testing.T
	home   string
	procs  fakeProcesses
	source *Source
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	procs := fakeProcesses{}
	return &fixture{t: t, home: home, procs: procs, source: NewSource(home, procs)}
}

// addSession writes a registry record for a running process.
func (f *fixture) addSession(pid int, fields map[string]any) {
	f.t.Helper()
	started := now.Add(-time.Hour)
	f.procs[pid] = started.Add(400 * time.Millisecond)
	record := map[string]any{
		"pid":       pid,
		"sessionId": "session-" + strconv.Itoa(pid),
		"cwd":       "/work/project",
		"startedAt": started.UnixMilli(),
		"procStart": started.Format(procStartLayout),
		"status":    "idle",
	}
	for k, v := range fields {
		record[k] = v
	}
	writeJSON(f.t, filepath.Join(f.source.SessionsDir, strconv.Itoa(pid)+".json"), record)
}

func (f *fixture) addTranscript(sessionID string, entries ...map[string]any) {
	f.t.Helper()
	var lines []string
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			f.t.Fatal(err)
		}
		lines = append(lines, string(data))
	}
	path := filepath.Join(f.source.ProjectsDir, "-work-project", sessionID+".jsonl")
	writeFile(f.t, path, strings.Join(lines, "\n")+"\n")
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
	recent := now.Add(-time.Minute).UnixMilli()
	tests := []struct {
		name   string
		fields map[string]any
		want   agent.Status
	}{
		{"busy is working", map[string]any{"status": "busy", "statusUpdatedAt": recent}, agent.StatusWorking},
		{"idle prompt is waiting", map[string]any{"statusUpdatedAt": recent}, agent.StatusWaiting},
		{"permission prompt is waiting", map[string]any{"status": "busy", "waitingFor": "permission"}, agent.StatusWaiting},
		{"untouched for hours is idle", map[string]any{"statusUpdatedAt": now.Add(-3 * time.Hour).UnixMilli()}, agent.StatusIdle},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.addSession(100+i, tt.fields)
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

func TestSessionFields(t *testing.T) {
	f := newFixture(t)
	changed := now.Add(-42 * time.Second)
	f.addSession(4242, map[string]any{"cwd": "/work/agent-lights", "statusUpdatedAt": changed.UnixMilli()})

	s := f.sessions()[0]

	if s.ID != "claude:4242" || s.Kind != agent.KindClaude || s.Dir != "/work/agent-lights" {
		t.Errorf("unexpected identity: %+v", s)
	}
	if got := s.Age(now); got != 42*time.Second {
		t.Errorf("age = %v, want 42s", got)
	}
}

func TestSkipsStaleAndBackgroundRecords(t *testing.T) {
	f := newFixture(t)
	f.addSession(1, nil)
	f.addSession(2, map[string]any{"spare": true})
	f.addSession(3, map[string]any{"parkedJobId": "job-1"})
	f.addSession(4, nil)
	delete(f.procs, 4) // process exited, file left behind
	f.addSession(5, nil)
	f.procs[5] = now // pid reused by a newer process
	writeFile(t, filepath.Join(f.source.SessionsDir, "6.json"), "{not json")

	sessions := f.sessions()

	if len(sessions) != 1 || sessions[0].ID != "claude:1" {
		t.Errorf("sessions = %+v, want only claude:1", sessions)
	}
}

func TestErrorAndContextFromTranscript(t *testing.T) {
	f := newFixture(t)
	f.addSession(7, map[string]any{"sessionId": "abc", "statusUpdatedAt": now.UnixMilli()})
	f.addTranscript("abc",
		map[string]any{"type": "user", "message": map[string]any{"content": "secret prompt"}},
		map[string]any{"type": "assistant", "message": map[string]any{"usage": map[string]any{
			"input_tokens": 10, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 87000,
		}}},
		map[string]any{"type": "assistant", "isSidechain": true, "message": map[string]any{"usage": map[string]any{
			"input_tokens": 5,
		}}},
		map[string]any{"type": "assistant", "isApiErrorMessage": true},
	)

	s := f.sessions()[0]

	if s.Status != agent.StatusError {
		t.Errorf("status = %s, want error", s.Status)
	}
	if s.Context == nil || s.Context.Used != 88010 || s.Context.Limit != StandardContextWindow {
		t.Errorf("context = %+v, want 88010 of the standard window", s.Context)
	}
}

func TestContextLimit(t *testing.T) {
	inferred := &Source{}
	if got := inferred.contextLimit(150_000); got != StandardContextWindow {
		t.Errorf("150k used: limit %d, want standard", got)
	}
	if got := inferred.contextLimit(240_000); got != ExtendedContextWindow {
		t.Errorf("240k used: limit %d, want extended (it cannot fit the standard window)", got)
	}
	if got := (&Source{ContextWindow: 1_000_000}).contextLimit(10_000); got != 1_000_000 {
		t.Errorf("configured window ignored: %d", got)
	}
}

func TestWorkingSessionIsNeverError(t *testing.T) {
	f := newFixture(t)
	f.addSession(8, map[string]any{"sessionId": "busy1", "status": "busy"})
	f.addTranscript("busy1", map[string]any{"type": "assistant", "isApiErrorMessage": true})

	if got := f.sessions()[0].Status; got != agent.StatusWorking {
		t.Errorf("status = %s, want working", got)
	}
}

func TestMissingRegistryIsNotAnError(t *testing.T) {
	f := newFixture(t)
	if sessions := f.sessions(); len(sessions) != 0 {
		t.Errorf("sessions = %v, want none", sessions)
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
