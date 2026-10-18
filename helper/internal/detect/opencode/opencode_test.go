package opencode

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

var now = time.Date(2026, 10, 9, 16, 30, 0, 0, time.UTC)

type fakeProcesses []process.Info

func (f fakeProcesses) FindByName(_ context.Context, name string) ([]process.Info, error) {
	if name != "opencode" {
		return nil, nil
	}
	return f, nil
}

var errStore = errors.New("no such column: data")

// fakeStore serves canned history per target. messageErr and partErr, when
// set, are returned for every target.
type fakeStore struct {
	messages   map[Target]Message
	parts      map[Target]ToolPart
	messageErr error
	partErr    error
}

func (f fakeStore) LastMessage(_ context.Context, target Target) (Message, bool, error) {
	m, ok := f.messages[target]
	return m, ok && f.messageErr == nil, f.messageErr
}

func (f fakeStore) LastToolPart(_ context.Context, target Target) (ToolPart, bool, error) {
	p, ok := f.parts[target]
	return p, ok && f.partErr == nil, f.partErr
}

// fakeTabs serves the sessions OpenCode has open in each folder.
type fakeTabs map[string][]string

func (f fakeTabs) OpenTabs(_ context.Context, dir string) ([]string, error) {
	return f[dir], nil
}

// errTabs is a tab state that cannot be read at all.
type errTabs struct{ err error }

func (e errTabs) OpenTabs(_ context.Context, _ string) ([]string, error) { return nil, e.err }

// folder and tab are the targets the detector asks its store about.
func folder(dir string) Target { return Target{Dir: dir} }

func tab(dir, session string) Target { return Target{Dir: dir, Session: session} }

func TestStatusOf(t *testing.T) {
	running := now.Add(-time.Hour) // when the opencode process started
	tests := []struct {
		name    string
		msg     Message
		started time.Time
		want    agent.Status
	}{
		{"reply streaming", Message{Role: "assistant", UpdatedAt: now}, running, agent.StatusWorking},
		{"reply finished", Message{Role: "assistant", Completed: true, UpdatedAt: now}, running, agent.StatusWaiting},
		{"reply failed", Message{Role: "assistant", ErrorName: "APIError", UpdatedAt: now}, running, agent.StatusError},
		{"reply aborted", Message{Role: "assistant", ErrorName: abortedError, UpdatedAt: now}, running, agent.StatusWaiting},
		{"reply interrupted (OpenCode 2)", Message{Role: "assistant", ErrorName: "aborted", UpdatedAt: now}, running, agent.StatusWaiting},
		{"prompt just sent", Message{Role: "user", UpdatedAt: now.Add(-10 * time.Second)}, running, agent.StatusWorking},
		{"prompt long ago", Message{Role: "user", UpdatedAt: now.Add(-10 * time.Minute)}, running, agent.StatusWaiting},
		{"dormant", Message{Role: "assistant", Completed: true, UpdatedAt: now.Add(-3 * time.Hour)}, running, agent.StatusIdle},
		{"an old error is idle", Message{Role: "assistant", ErrorName: "APIError", UpdatedAt: now.Add(-3 * time.Hour)}, running, agent.StatusIdle},
		{"reply left by a previous run", Message{Role: "assistant", UpdatedAt: now.Add(-2 * time.Minute)}, now.Add(-time.Minute), agent.StatusWaiting},
		{"prompt left by a previous run", Message{Role: "user", UpdatedAt: now.Add(-10 * time.Second)}, now.Add(-5 * time.Second), agent.StatusWaiting},
		{"error left by a previous run", Message{Role: "assistant", ErrorName: "APIError", UpdatedAt: now.Add(-2 * time.Minute)}, now.Add(-time.Minute), agent.StatusWaiting},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusOf(tt.msg, tt.started, now); got != tt.want {
				t.Errorf("statusOf = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestOneCardPerOpenTab(t *testing.T) {
	started := now.Add(-time.Hour)
	source := &Source{
		Processes: fakeProcesses{
			{PID: 10, Dir: "/w/api", StartedAt: started},
			{PID: 13, Dir: "/w/web", StartedAt: started},
		},
		Store: fakeStore{messages: map[Target]Message{
			folder("/w/api"):       {Role: "assistant", ErrorName: "APIError", Completed: true, UpdatedAt: now},
			tab("/w/api", "ses_a"): {Role: "user", UpdatedAt: now, TurnStartedAt: now.Add(-time.Minute)},
			tab("/w/api", "ses_b"): {Role: "assistant", ErrorName: "APIError", Completed: true, UpdatedAt: now},
		}},
		Tabs: fakeTabs{"/w/api": {"ses_a", "ses_b"}},
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want the two tabs and the folder without one: %+v", len(sessions), sessions)
	}
	byID := make(map[string]agent.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID] = s
	}
	if s, ok := byID["opencode:ses_a"]; !ok || s.Dir != "/w/api" || s.Status != agent.StatusWorking {
		t.Errorf("first tab = %+v, want the working one in /w/api", s)
	}
	if s, ok := byID["opencode:ses_b"]; !ok || s.Status != agent.StatusError {
		t.Errorf("second tab = %+v, want its own error, not the other tab's status", s)
	}
	if s, ok := byID["opencode:13"]; !ok || s.Status != agent.StatusWaiting {
		t.Errorf("folder without tabs = %+v, want the folder card, waiting", s)
	}
	if _, ok := byID["opencode:10"]; ok {
		t.Error("the folder with tabs still got a card of its own")
	}
}

func TestTabCardsAreNamedInTabOrder(t *testing.T) {
	started := now.Add(-time.Hour)
	source := &Source{
		Processes: fakeProcesses{{PID: 10, Dir: "/w/api", StartedAt: started}},
		Store: fakeStore{messages: map[Target]Message{
			// Offered in tab order, not in session-id order.
			tab("/w/api", "ses_c"): {Role: "assistant", Completed: true, UpdatedAt: now},
			tab("/w/api", "ses_a"): {Role: "assistant", Completed: true, UpdatedAt: now},
			tab("/w/api", "ses_b"): {Role: "assistant", Completed: true, UpdatedAt: now},
		}},
		Tabs: fakeTabs{"/w/api": {"ses_c", "ses_a", "ses_b"}},
	}

	detected, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}

	ids := make([]string, 0, len(detected))
	names := make([]string, 0, len(detected))
	for _, card := range agent.Arrange(detected) {
		ids = append(ids, card.ID)
		names = append(names, card.Name)
	}
	if want := []string{"opencode:ses_c", "opencode:ses_a", "opencode:ses_b"}; !slices.Equal(ids, want) {
		t.Errorf("cards = %v, want the tab order %v", ids, want)
	}
	if want := []string{"api #1", "api #2", "api #3"}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v, #1 for the first tab", names, want)
	}
}

func TestOneCardPerFolderWithoutTabState(t *testing.T) {
	started := now.Add(-time.Hour)
	source := &Source{
		Processes: fakeProcesses{
			{PID: 10, Dir: "/w/api", StartedAt: started},
			{PID: 11, Dir: "/w/api", StartedAt: started.Add(-time.Minute)},
			{PID: 12, Dir: "", StartedAt: started},
			{PID: 13, Dir: "/w/web", StartedAt: started},
		},
		Store: fakeStore{messages: map[Target]Message{
			folder("/w/api"): {Role: "assistant", UpdatedAt: now.Add(-time.Second), TurnStartedAt: now.Add(-5 * time.Second)},
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
	if api.ID != "opencode:11" || api.Status != agent.StatusWorking {
		t.Errorf("api session = %+v, want the oldest process, working", api)
	}
	if web.Status != agent.StatusWaiting || !web.Since.Equal(started) {
		t.Errorf("web session without history = %+v", web)
	}
}

func TestOneCardPerFolderWhenTabsAreEmptyOrUnreadable(t *testing.T) {
	started := now.Add(-time.Hour)
	unreadable := errors.New("open tabs.json: permission denied")
	tests := []struct {
		name    string
		tabs    Tabs
		wantErr error
	}{
		{"no tabs open", fakeTabs{}, nil},
		{"a folder the state does not know", fakeTabs{"/elsewhere": {"ses_a"}}, nil},
		{"unreadable state", errTabs{err: unreadable}, unreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &Source{
				Processes: fakeProcesses{{PID: 10, Dir: "/w/api", StartedAt: started}},
				Store: fakeStore{messages: map[Target]Message{
					folder("/w/api"): {Role: "assistant", UpdatedAt: now, TurnStartedAt: now},
				}},
				Tabs: tt.tabs,
			}

			sessions, err := source.Sessions(context.Background(), now)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if len(sessions) != 1 || sessions[0].ID != "opencode:10" || sessions[0].Status != agent.StatusWorking {
				t.Errorf("sessions = %+v, want one card for the folder, working", sessions)
			}
		})
	}
}

func TestTheBackgroundServerIsNotASession(t *testing.T) {
	started := now.Add(-time.Hour)
	tests := []struct {
		name string
		args []string
		card bool
	}{
		{"terminal", []string{"opencode"}, true},
		{"a prompt that says serve", []string{"opencode", "run", "serve"}, true},
		{"not readable", nil, true},
		{"service", []string{"/home/ann/.opencode/bin/opencode", "serve", "--service"}, false},
		{"service after a flag", []string{"opencode", "--print-logs", "serve"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &Source{
				Processes: fakeProcesses{{PID: 1, Dir: "/home/ann", Args: tt.args, StartedAt: started}},
				Store:     fakeStore{},
				Tabs:      fakeTabs{"/home/ann": {"ses_a", "ses_b"}},
			}

			sessions, err := source.Sessions(context.Background(), now)

			if err != nil || (len(sessions) > 0) != tt.card {
				t.Errorf("sessions = %+v, %v; want a card: %v", sessions, err, tt.card)
			}
		})
	}
}

func TestWorkingAgeCountsFromThePrompt(t *testing.T) {
	prompt := now.Add(-4 * time.Minute)
	source := &Source{
		Processes: fakeProcesses{{PID: 1, Dir: "/w/api", StartedAt: now.Add(-time.Hour)}},
		Store: fakeStore{messages: map[Target]Message{
			// The third reply of the turn, still streaming.
			folder("/w/api"): {Role: "assistant", UpdatedAt: now.Add(-time.Second), TurnStartedAt: prompt},
		}},
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if s := sessions[0]; s.Status != agent.StatusWorking || s.Age(now) != 4*time.Minute {
		t.Errorf("session = %+v, want working for 4m", s)
	}
}

func TestReplyFromAPreviousRunIsNotWorking(t *testing.T) {
	source := &Source{
		Processes: fakeProcesses{{PID: 1, Dir: "/w/api", StartedAt: now.Add(-time.Minute)}},
		Store: fakeStore{messages: map[Target]Message{
			// Never completed: the previous opencode in this folder was killed.
			folder("/w/api"): {Role: "assistant", UpdatedAt: now.Add(-time.Hour), TurnStartedAt: now.Add(-time.Hour)},
		}},
	}

	sessions, err := source.Sessions(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].Status != agent.StatusWaiting {
		t.Errorf("status = %s, want waiting", sessions[0].Status)
	}
}

func TestStaleRunningToolMeansWaiting(t *testing.T) {
	tests := []struct {
		name string
		part ToolPart
		want agent.Status
	}{
		{"a tool frozen at running", ToolPart{State: "running", UpdatedAt: now.Add(-time.Minute)}, agent.StatusWaiting},
		{"a tool that keeps updating", ToolPart{State: "running", UpdatedAt: now}, agent.StatusWorking},
		{"a finished tool", ToolPart{State: "completed", UpdatedAt: now.Add(-time.Minute)}, agent.StatusWorking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &Source{
				Processes: fakeProcesses{{PID: 1, Dir: "/w/api", StartedAt: now.Add(-time.Hour)}},
				Store: fakeStore{
					messages: map[Target]Message{tab("/w/api", "ses_a"): {Role: "assistant", UpdatedAt: now}},
					parts:    map[Target]ToolPart{tab("/w/api", "ses_a"): tt.part},
				},
				Tabs: fakeTabs{"/w/api": {"ses_a"}},
			}

			sessions, err := source.Sessions(context.Background(), now)
			if err != nil {
				t.Fatal(err)
			}
			if sessions[0].Status != tt.want {
				t.Errorf("status = %s, want %s", sessions[0].Status, tt.want)
			}
		})
	}
}

func TestStoreErrorsAreReportedButSessionsShown(t *testing.T) {
	working := map[Target]Message{
		tab("/w/api", "ses_a"): {Role: "assistant", UpdatedAt: now, TurnStartedAt: now},
		tab("/w/api", "ses_b"): {Role: "assistant", UpdatedAt: now, TurnStartedAt: now},
		folder("/w/web"):       {Role: "assistant", UpdatedAt: now, TurnStartedAt: now},
	}
	tests := []struct {
		name  string
		store fakeStore
		want  agent.Status
	}{
		{"messages unreadable", fakeStore{messages: working, messageErr: errStore}, agent.StatusWaiting},
		{"tool calls unreadable", fakeStore{messages: working, partErr: errStore}, agent.StatusWorking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &Source{
				Processes: fakeProcesses{
					{PID: 1, Dir: "/w/api", StartedAt: now.Add(-time.Hour)},
					{PID: 2, Dir: "/w/web", StartedAt: now.Add(-time.Hour)},
				},
				Store: tt.store,
				Tabs:  fakeTabs{"/w/api": {"ses_a", "ses_b"}},
			}

			sessions, err := source.Sessions(context.Background(), now)

			if !errors.Is(err, errStore) {
				t.Errorf("err = %v, want the store error", err)
			}
			if len(sessions) != 3 {
				t.Fatalf("got %d sessions, want both tabs and the folder shown: %+v", len(sessions), sessions)
			}
			for _, s := range sessions {
				if s.Status != tt.want {
					t.Errorf("%s: status = %s, want %s", s.ID, s.Status, tt.want)
				}
			}
		})
	}
}
