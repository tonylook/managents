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

// opened is when the tests' session processes began, and when their
// conversations were opened.
var opened = now.Add(-time.Hour)

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

// conversation is a folder's conversation the store answers with, written to
// last at updated.
func conversation(id string, updated time.Time) Session {
	return Session{ID: id, CreatedAt: opened, UpdatedAt: updated}
}

// emptyStore has no history at all, like a ZCode that never ran.
type emptyStore struct{}

func (emptyStore) Sessions(context.Context, string) ([]Session, error) {
	return nil, nil
}
func (emptyStore) LastTurn(context.Context, string) (Turn, bool, error) { return Turn{}, false, nil }
func (emptyStore) LastInput(context.Context, string) (Input, bool, error) {
	return Input{}, false, nil
}

// fakeStore serves the conversations of every folder, the one written to
// last first, and each conversation's newest prompt and turn. A missing Turn
// or Input means none.
type fakeStore struct {
	sessions map[string][]Session
	inputs   map[string]Input
	turns    map[string]Turn
}

func (f fakeStore) Sessions(_ context.Context, dir string) ([]Session, error) {
	return f.sessions[dir], nil
}

func (f fakeStore) LastTurn(_ context.Context, sessionID string) (Turn, bool, error) {
	turn, ok := f.turns[sessionID]
	return turn, ok, nil
}

func (f fakeStore) LastInput(_ context.Context, sessionID string) (Input, bool, error) {
	input, ok := f.inputs[sessionID]
	return input, ok, nil
}

// busyStore finds the conversations but cannot read their history, the way a
// locked database does.
type busyStore struct{ fakeStore }

func (busyStore) LastTurn(context.Context, string) (Turn, bool, error) { return Turn{}, false, errBusy }
func (busyStore) LastInput(context.Context, string) (Input, bool, error) {
	return Input{}, false, errBusy
}

// lockedStore cannot even say which conversations a folder has.
type lockedStore struct{}

func (lockedStore) Sessions(context.Context, string) ([]Session, error) { return nil, errBusy }
func (lockedStore) LastTurn(context.Context, string) (Turn, bool, error) {
	return Turn{}, false, errBusy
}
func (lockedStore) LastInput(context.Context, string) (Input, bool, error) {
	return Input{}, false, errBusy
}

var errBusy = errors.New("database is locked")

// source builds a Source over the given processes and store.
func source(procs fakeProcesses, store Store) *Source {
	return &Source{Processes: procs, Store: store, dataDir: dataDir}
}

func TestSessions(t *testing.T) {
	api := cli(7, "/w/api", opened)
	web := cli(9, "/w/web", opened)
	history := fakeStore{
		sessions: map[string][]Session{
			"/w/api": {conversation("s-api", now.Add(-10*time.Second))},
			"/w/web": {conversation("s-web", now.Add(-50*time.Second))},
		},
		inputs: map[string]Input{"s-api": {SentAt: now.Add(-10 * time.Second)}},
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
			wantIDs: []string{"zcode:s-api"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name:    "an ended turn waits since it ended",
			procs:   fakeProcesses{web},
			store:   history,
			wantIDs: []string{"zcode:s-web"},
			want:    []agent.Status{agent.StatusWaiting},
		},
		{
			name: "a card per conversation, not one per folder",
			procs: fakeProcesses{
				api,
				cli(8, "/w/api", opened.Add(-time.Minute)), // a second session in the same folder
			},
			store: fakeStore{
				sessions: map[string][]Session{"/w/api": {
					conversation("s-api", now.Add(-10*time.Second)),
					conversation("s-second", now.Add(-2*time.Minute)),
				}},
				inputs: map[string]Input{"s-api": {SentAt: now.Add(-10 * time.Second)}},
				turns: map[string]Turn{"s-second": {
					Status: "completed", StartedAt: now.Add(-3 * time.Minute), CompletedAt: now.Add(-2 * time.Minute),
				}},
			},
			wantIDs: []string{"zcode:s-api", "zcode:s-second"},
			want:    []agent.Status{agent.StatusWorking, agent.StatusWaiting},
		},
		{
			name:  "a conversation quiet beyond five minutes gets no card",
			procs: fakeProcesses{api},
			store: fakeStore{
				sessions: map[string][]Session{"/w/api": {
					conversation("s-api", now.Add(-10*time.Second)),
					conversation("s-closed", now.Add(-quietFor-time.Minute)),
				}},
				inputs: map[string]Input{"s-api": {SentAt: now.Add(-10 * time.Second)}},
				turns: map[string]Turn{"s-closed": {
					Status: "completed", StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-quietFor - time.Minute),
				}},
			},
			wantIDs: []string{"zcode:s-api"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name:  "a folder whose conversations all went quiet is not shown",
			procs: fakeProcesses{web},
			store: fakeStore{
				sessions: map[string][]Session{"/w/web": {
					conversation("s-web", now.Add(-quietFor-time.Minute)),
				}},
				turns: map[string]Turn{"s-web": {
					Status: "completed", StartedAt: now.Add(-time.Hour), CompletedAt: now.Add(-quietFor - time.Minute),
				}},
			},
			wantIDs: nil,
			want:    nil,
		},
		{
			name:  "a conversation still working is kept whatever its write age",
			procs: fakeProcesses{api},
			store: fakeStore{
				sessions: map[string][]Session{"/w/api": {
					conversation("s-api", now.Add(-30*time.Minute)), // written when the prompt was admitted
				}},
				inputs: map[string]Input{"s-api": {SentAt: now.Add(-30 * time.Minute)}},
			},
			wantIDs: []string{"zcode:s-api"},
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
			wantIDs: []string{"zcode:s-api"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name: "a folder in ZCode's own data dir is not a session",
			procs: fakeProcesses{
				cli(30, dataDir, opened),                                     // the app's runner
				cli(31, dataDir+"/workspace/1794155162721-1b0acec4", opened), // an automation copy
				api,
			},
			store:   history,
			wantIDs: []string{"zcode:s-api"},
			want:    []agent.Status{agent.StatusWorking},
		},
		{
			name: "the binary itself is a session, without a command line",
			procs: fakeProcesses{
				{PID: 40, Name: "zcode-cli", Dir: "/w/api", StartedAt: opened},
			},
			store:   history,
			wantIDs: []string{"zcode:s-api"},
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
			name: "a locked database still shows the conversations",
			procs: fakeProcesses{
				api, web, {PID: 12, Name: "ZCode Helper", Dir: "", Args: []string{"zcode-cli"}, StartedAt: opened}, // no folder: no card
			},
			store:   busyStore{fakeStore{sessions: history.sessions}},
			wantIDs: []string{"zcode:s-api", "zcode:s-web"},
			want:    []agent.Status{agent.StatusWaiting, agent.StatusWaiting},
			wantErr: errBusy,
		},
		{
			name:    "a database that cannot be opened still shows the folder",
			procs:   fakeProcesses{api},
			store:   lockedStore{},
			wantIDs: []string{"zcode:7"},
			want:    []agent.Status{agent.StatusWaiting},
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
		fakeProcesses{cli(7, "/w/api", opened)},
		fakeStore{
			sessions: map[string][]Session{"/w/api": {conversation("s-api", sent)}},
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
	tests := []struct {
		name     string
		hasInput bool
		input    Input
		turn     Turn
		want     agent.Status
		wantAge  time.Duration // how long ago Since is
	}{
		{"a prompt just sent, no turn yet", true, Input{SentAt: now.Add(-10 * time.Second)}, Turn{},
			agent.StatusWorking, 10 * time.Second},
		{"a prompt sent while an earlier turn runs", true, Input{SentAt: now.Add(-10 * time.Second)}, Turn{
			StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-30 * time.Second)},
			agent.StatusWorking, 10 * time.Second},
		{"a turn ZCode has not closed", true, Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "running", StartedAt: now.Add(-10 * time.Second)},
			agent.StatusWorking, 10 * time.Second},
		{"an ended turn", true, Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "completed", StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-50 * time.Second)},
			agent.StatusWaiting, 50 * time.Second},
		{"a failed turn", true, Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "error", StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-45 * time.Second)},
			agent.StatusError, 45 * time.Second},
		{"a turn the user cancelled", true, Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "cancelled", CancelledByUser: true, StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-40 * time.Second)},
			agent.StatusWaiting, 40 * time.Second},
		{"a failure the user interrupted", true, Input{SentAt: now.Add(-time.Minute)}, Turn{
			Status: "error", CancelledByUser: true, StartedAt: now.Add(-time.Minute), CompletedAt: now.Add(-40 * time.Second)},
			agent.StatusWaiting, 40 * time.Second},
		{"a prompt never answered is not working", true, Input{SentAt: now.Add(-3 * time.Hour)}, Turn{},
			agent.StatusWaiting, time.Hour}, // the app died mid-turn; since it was opened
		{"an unclosed turn of a dead app is not working", true, Input{SentAt: now.Add(-3 * time.Hour)}, Turn{
			Status: "running", StartedAt: now.Add(-3 * time.Hour)},
			agent.StatusWaiting, time.Hour},
		{"no history at all", false, Input{}, Turn{},
			agent.StatusWaiting, time.Hour}, // since the conversation was opened
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, since := statusOf(tt.hasInput, tt.input, tt.turn != Turn{}, tt.turn, opened, now)

			if status != tt.want {
				t.Errorf("status = %s, want %s", status, tt.want)
			}
			if age := now.Sub(since); age != tt.wantAge {
				t.Errorf("since = %s ago, want %s ago", age, tt.wantAge)
			}
		})
	}
}
