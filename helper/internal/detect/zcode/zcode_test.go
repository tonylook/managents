package zcode

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/process"
)

var now = time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)

// dataDir is ZCode's own folder in the tests' home.
const dataDir = "/home/u/.zcode"

// fakeProcesses answers for both names a session process can have: the
// binary, and the desktop app's Electron helpers.
type fakeProcesses []process.Info

func (f fakeProcesses) FindByName(_ context.Context, name string) ([]process.Info, error) {
	var found []process.Info
	for _, p := range f {
		if p.Name == name {
			found = append(found, p)
		}
	}
	return found, nil
}

// cli is a session process of the desktop app: an Electron helper whose
// title ZCode rewrote. started is when it began.
func cli(pid int, dir string, started time.Time) process.Info {
	return process.Info{PID: pid, Name: "ZCode Helper", Dir: dir, Args: []string{"zcode-cli"}, StartedAt: started}
}

// emptyStore has no history at all, like a ZCode that never ran.
type emptyStore struct{}

func (emptyStore) LastSession(context.Context, string) (Session, bool, error) {
	return Session{}, false, nil
}
func (emptyStore) LastTurn(context.Context, string) (Turn, bool, error)   { return Turn{}, false, nil }
func (emptyStore) LastInput(context.Context, string) (Input, bool, error) { return Input{}, false, nil }

// fakeStore serves the conversation of every folder: one session id, its
// newest prompt and newest turn. A missing Turn or Input means none.
type fakeStore struct {
	sessions map[string]Session
	inputs   map[string]Input
	turns    map[string]Turn
}

func (f fakeStore) LastSession(_ context.Context, dir string) (Session, bool, error) {
	s, ok := f.sessions[dir]
	return s, ok, nil
}

func (f fakeStore) LastTurn(_ context.Context, sessionID string) (Turn, bool, error) {
	turn, ok := f.turns[sessionID]
	return turn, ok, nil
}

func (f fakeStore) LastInput(_ context.Context, sessionID string) (Input, bool, error) {
	input, ok := f.inputs[sessionID]
	return input, ok, nil
}

// busyStore finds the conversation but cannot read its history, the way a
// locked database does.
type busyStore struct{ fakeStore }

func (busyStore) LastTurn(context.Context, string) (Turn, bool, error) { return Turn{}, false, errBusy }
func (busyStore) LastInput(context.Context, string) (Input, bool, error) {
	return Input{}, false, errBusy
}

var errBusy = errors.New("database is locked")

// source builds a Source over the given processes and store.
func source(procs fakeProcesses, store Store) *Source {
	return &Source{Processes: procs, Store: store, dataDir: dataDir}
}

func TestSessions(t *testing.T) {
	opened := now.Add(-time.Hour) // when the folder's process started
	api := cli(7, "/w/api", opened)
	web := cli(9, "/w/web", opened)
	history := fakeStore{
		sessions: map[string]Session{"/w/api": {ID: "s-api"}, "/w/web": {ID: "s-web"}},
		inputs:   map[string]Input{"s-api": {SentAt: now.Add(-10 * time.Second)}},
		turns: map[string]Turn{"s-web": {
			Status: "completed", StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-50 * time.Second),
		}},
	}
	tests := []struct {
		name    string
		procs   fakeProcesses
		store   Store
		wantIDs []string
		want    []agent.Status
		wantErr error
	}{
		{
			name:    "a prompt without its turn is working",
			procs:   fakeProcesses{api},
			store:   history,
			wantIDs: []string{"zcode:7"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name:    "an ended turn waits since it ended",
			procs:   fakeProcesses{web},
			store:   history,
			wantIDs: []string{"zcode:9"},
			want:    []agent.Status{agent.StatusWaiting},
		},
		{
			name: "the oldest process owns the folder",
			procs: fakeProcesses{
				cli(8, "/w/api", now.Add(-time.Minute)),
				api,
			},
			store:   history,
			wantIDs: []string{"zcode:7"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name: "the app's own helpers and services are not sessions",
			procs: fakeProcesses{
				{PID: 20, Name: "ZCode Helper", Dir: "/", Args: []string{
					"/Applications/ZCode.app/Contents/Frameworks/ZCode Helper.app/Contents/MacOS/ZCode Helper",
					"--type=gpu-process"}, StartedAt: opened},
				{PID: 21, Name: "ZCode Helper", Dir: "/", Args: []string{"zcode-host-local-1"}, StartedAt: opened},
				{PID: 22, Name: "ZCode Helper", Dir: "/w/api", Args: []string{"zcode-node-repl-mcp"}, StartedAt: opened},
				api,
			},
			store:   history,
			wantIDs: []string{"zcode:7"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name: "a folder in ZCode's own data dir is not a session",
			procs: fakeProcesses{
				cli(30, dataDir, opened), // the app's runner
				cli(31, dataDir+"/workspace/1794155162721-1b0acec4", opened), // an automation copy
				api,
			},
			store:   history,
			wantIDs: []string{"zcode:7"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name: "the binary itself is a session, without a command line",
			procs: fakeProcesses{
				{PID: 40, Name: "zcode-cli", Dir: "/w/api", StartedAt: opened},
			},
			store:   history,
			wantIDs: []string{"zcode:40"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name:    "no history: waiting since the process started",
			procs:   fakeProcesses{api},
			store:   emptyStore{},
			wantIDs: []string{"zcode:7"},
			want:    []agent.Status{agent.StatusWaiting},
		},
		{
			name: "a busy database still shows the folder",
			procs: fakeProcesses{
				api, web, {PID: 12, Name: "ZCode Helper", Dir: "", Args: []string{"zcode-cli"}, StartedAt: opened}, // no folder: no card
			},
			store:   busyStore{fakeStore{sessions: map[string]Session{"/w/api": {ID: "s-api"}, "/w/web": {ID: "s-web"}}}},
			wantIDs: []string{"zcode:7", "zcode:9"},
			want:    []agent.Status{agent.StatusWaiting, agent.StatusWaiting},
			wantErr: errBusy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions, err := source(tt.procs, tt.store).Sessions(context.Background(), now)

			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			ids := make([]string, len(sessions))
			statuses := make([]agent.Status, len(sessions))
			for i, s := range sessions {
				ids[i], statuses[i] = s.ID, s.Status
				if s.Kind != agent.KindZCode {
					t.Errorf("session %s: kind = %s, want zcode", s.ID, s.Kind)
				}
				if s.StartedAt.IsZero() || s.Since.IsZero() {
					t.Errorf("session %s: StartedAt or Since is zero", s.ID)
				}
			}
			if !slices.Equal(ids, tt.wantIDs) || !slices.Equal(statuses, tt.want) {
				t.Errorf("sessions = %s %s, want %s %s", ids, statuses, tt.wantIDs, tt.want)
			}
		})
	}
}

func TestWorkingCountsFromThePrompt(t *testing.T) {
	sent := now.Add(-3 * time.Minute)
	sessions, err := source(
		fakeProcesses{cli(7, "/w/api", now.Add(-time.Hour))},
		fakeStore{
			sessions: map[string]Session{"/w/api": {ID: "s-api"}},
			inputs:   map[string]Input{"s-api": {SentAt: sent}},
		},
	).Sessions(context.Background(), now)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v, %v; want one", sessions, err)
	}
	if sessions[0].Status != agent.StatusWorking || !sessions[0].Since.Equal(sent) {
		t.Errorf("session = %+v, want working since the prompt", sessions[0])
	}
	if sessions[0].Dir != "/w/api" {
		t.Errorf("Dir = %s, want the process's folder", sessions[0].Dir)
	}
}

func TestStatusOf(t *testing.T) {
	opened := now.Add(-time.Hour) // when the process started
	tests := []struct {
		name    string
		input   Input
		turn    Turn
		started time.Time // zero: the process started an hour ago
		want    agent.Status
		wantAge time.Duration // how long ago Since is
	}{
		{"a prompt just sent, no turn yet", Input{SentAt: now.Add(-10 * time.Second)}, Turn{},
			time.Time{}, agent.StatusWorking, 10 * time.Second},
		{"a prompt sent while an earlier turn runs", Input{SentAt: now.Add(-10 * time.Second)}, Turn{
			StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-30 * time.Second)},
			time.Time{}, agent.StatusWorking, 10 * time.Second},
		{"a turn ZCode has not closed", Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "running", StartedAt: now.Add(-10 * time.Second)},
			time.Time{}, agent.StatusWorking, 10 * time.Second},
		{"an ended turn", Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "completed", StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-50 * time.Second)},
			time.Time{}, agent.StatusWaiting, 50 * time.Second},
		{"a failed turn", Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "error", StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-45 * time.Second)},
			time.Time{}, agent.StatusError, 45 * time.Second},
		{"a turn the user cancelled", Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "cancelled", CancelledByUser: true, StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-40 * time.Second)},
			time.Time{}, agent.StatusWaiting, 40 * time.Second},
		{"a failure the user interrupted", Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "error", CancelledByUser: true, StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-40 * time.Second)},
			time.Time{}, agent.StatusWaiting, 40 * time.Second},
		{"an ended turn two hours old", Input{SentAt: now.Add(-3 * time.Hour)}, Turn{
			Status: "completed", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3 * time.Hour)},
			now.Add(-4 * time.Hour), agent.StatusIdle, 3 * time.Hour},
		{"an old error is idle", Input{SentAt: now.Add(-3 * time.Hour)}, Turn{
			Status: "error", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3 * time.Hour)},
			now.Add(-4 * time.Hour), agent.StatusIdle, 3 * time.Hour},
		{"a prompt of a previous run is not working", Input{SentAt: opened.Add(-time.Minute)}, Turn{},
			time.Time{}, agent.StatusWaiting, time.Hour},
		{"an error of a previous run is not this run's", Input{SentAt: opened.Add(-2 * time.Minute)}, Turn{
			Status: "error", StartedAt: opened.Add(-2 * time.Minute), CompletedAt: opened.Add(-2 * time.Minute)},
			time.Time{}, agent.StatusWaiting, time.Hour},
		{"no history at all", Input{}, Turn{},
			time.Time{}, agent.StatusWaiting, time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := tt.started
			if started.IsZero() {
				started = opened
			}
			status, since := statusOf(true, tt.input, tt.turn != Turn{}, tt.turn, started, now)

			if status != tt.want {
				t.Errorf("status = %s, want %s", status, tt.want)
			}
			if age := now.Sub(since); age != tt.wantAge {
				t.Errorf("since = %s ago, want %s ago", age, tt.wantAge)
			}
		})
	}

	t.Run("nothing at all", func(t *testing.T) {
		status, since := statusOf(false, Input{}, false, Turn{}, opened, now)
		if status != agent.StatusWaiting || now.Sub(since) != time.Hour {
			t.Errorf("status, since = %s, %s ago; want waiting since the process started", status, now.Sub(since))
		}
	})
}
