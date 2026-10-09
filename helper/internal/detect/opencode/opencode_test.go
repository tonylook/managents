package opencode

import (
	"context"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

var now = time.Date(2026, 10, 9, 16, 30, 0, 0, time.UTC)

type fakeProcesses []process.Info

func (f fakeProcesses) StartTime(context.Context, int) (time.Time, bool) { return time.Time{}, false }

func (f fakeProcesses) FindByName(_ context.Context, name string) ([]process.Info, error) {
	if name != "opencode" {
		return nil, nil
	}
	return f, nil
}

type fakeStore struct {
	messages map[string]Message
	parts    map[string]ToolPart
}

func (f fakeStore) LastMessage(_ context.Context, dir string) (Message, bool, error) {
	m, ok := f.messages[dir]
	return m, ok, nil
}

func (f fakeStore) LastToolPart(_ context.Context, dir string) (ToolPart, bool, error) {
	p, ok := f.parts[dir]
	return p, ok, nil
}

func TestStatusOf(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want agent.Status
	}{
		{"reply streaming", Message{Role: "assistant", UpdatedAt: now}, agent.StatusWorking},
		{"reply finished", Message{Role: "assistant", Completed: true, UpdatedAt: now}, agent.StatusWaiting},
		{"reply failed", Message{Role: "assistant", ErrorName: "APIError", UpdatedAt: now}, agent.StatusError},
		{"reply aborted", Message{Role: "assistant", ErrorName: abortedError, UpdatedAt: now}, agent.StatusWaiting},
		{"prompt just sent", Message{Role: "user", UpdatedAt: now.Add(-10 * time.Second)}, agent.StatusWorking},
		{"prompt long ago", Message{Role: "user", UpdatedAt: now.Add(-10 * time.Minute)}, agent.StatusWaiting},
		{"dormant", Message{Role: "assistant", Completed: true, UpdatedAt: now.Add(-3 * time.Hour)}, agent.StatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusOf(tt.msg, now); got != tt.want {
				t.Errorf("statusOf = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSessionsOnePerFolder(t *testing.T) {
	started := now.Add(-time.Hour)
	source := &Source{
		Processes: fakeProcesses{
			{PID: 10, Dir: "/w/api", StartedAt: started},
			{PID: 11, Dir: "/w/api", StartedAt: started},
			{PID: 12, Dir: "", StartedAt: started},
			{PID: 13, Dir: "/w/web", StartedAt: started},
		},
		Store: fakeStore{messages: map[string]Message{
			"/w/api": {Role: "assistant", UpdatedAt: now.Add(-5 * time.Second)},
		}},
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}
	api, web := sessions[0], sessions[1]
	if api.ID != "opencode:10" || api.Status != agent.StatusWorking || api.Age(now) != 5*time.Second {
		t.Errorf("api session = %+v", api)
	}
	if web.Status != agent.StatusWaiting || !web.Since.Equal(started) {
		t.Errorf("web session without history = %+v", web)
	}
}

func TestStaleRunningToolMeansWaiting(t *testing.T) {
	source := &Source{
		Processes: fakeProcesses{{PID: 1, Dir: "/w/api", StartedAt: now}},
		Store: fakeStore{
			messages: map[string]Message{"/w/api": {Role: "assistant", UpdatedAt: now}},
			parts:    map[string]ToolPart{"/w/api": {State: "running", UpdatedAt: now.Add(-time.Minute)}},
		},
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting", sessions[0].Status)
	}
}
