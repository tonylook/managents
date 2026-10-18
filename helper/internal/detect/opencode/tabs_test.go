package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/protocol"
)

// writeTabs writes an OpenCode tab state file and returns the reader for it.
func writeTabs(t *testing.T, content string) StateTabs {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tabs.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return StateTabs{Path: path}
}

func TestStateTabsReadsTheTabsOfADirectory(t *testing.T) {
	tabs := writeTabs(t, `{
	  "global": {"tabs": [{"sessionID": "ses_global"}]},
	  "cwd": {
	    "/w/api": {"tabs": [{"sessionID": "ses_a"}, {"sessionID": "ses_b"}]},
	    "/w/web": {"tabs": [{"sessionID": "ses_c"}]}
	  }
	}`)
	ctx := context.Background()

	tests := []struct {
		name string
		dir  string
		want []string
	}{
		{"two tabs", "/w/api", []string{"ses_a", "ses_b"}},
		{"another folder", "/w/web", []string{"ses_c"}},
		{"a closed folder", "/w/docs", nil},
		{"a trailing separator", "/w/api/", []string{"ses_a", "ses_b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tabs.OpenTabs(ctx, tt.dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("OpenTabs(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}

func TestStateTabsSkipsEmptyAndRepeatedIds(t *testing.T) {
	tabs := writeTabs(t, `{"cwd": {"/w/api": {"tabs": [
	  {"sessionID": "ses_a"}, {"sessionID": ""}, {"sessionID": "ses_a"}, {}]}}}`)

	got, err := tabs.OpenTabs(context.Background(), "/w/api")

	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ses_a"}; !slices.Equal(got, want) {
		t.Errorf("OpenTabs = %v, want %v", got, want)
	}
}

func TestStateTabsWithoutAUsableFile(t *testing.T) {
	unreadable := filepath.Join(t.TempDir(), "tabs.json")
	if err := os.MkdirAll(unreadable, 0o700); err != nil { // a directory, not a file
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		tabs    StateTabs
		wantErr bool
	}{
		{"no file", StateTabs{Path: filepath.Join(t.TempDir(), "absent.json")}, false},
		{"empty file", writeTabs(t, ""), true},
		{"truncated", writeTabs(t, `{"cwd": {`), true},
		{"unreadable", StateTabs{Path: unreadable}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.tabs.OpenTabs(context.Background(), "/w/api")

			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, want an error: %v", err, tt.wantErr)
			}
			if got != nil {
				t.Errorf("OpenTabs = %v, want nothing", got)
			}
		})
	}
}

func TestDefaultTabsPath(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	tests := []struct {
		name, env, want string
	}{
		{"default", "", filepath.Join(home, ".local", "state", "opencode", "latest", "tui", "tabs.json")},
		{"XDG_STATE_HOME", state, filepath.Join(state, "opencode", "latest", "tui", "tabs.json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tt.env)
			if got := DefaultTabsPath(home); got != tt.want {
				t.Errorf("DefaultTabsPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConversationContentNeverLeaksFromOpenCodeTabs(t *testing.T) {
	const canary = "CANARY-t4bs"
	path, db := newDatabaseV2(t)
	at := now.UnixMilli()
	exec(t, db, `INSERT INTO session_v2 (id, directory, time_updated) VALUES ('ses_a', '/w/api', ?)`, at)
	exec(t, db, `INSERT INTO session_message VALUES ('a1', 'ses_a', 'assistant', 1, ?, ?,
		'{"content":[{"type":"text","text":"x"}]}')`, at-1000, at)
	tabs := writeTabs(t, `{"global": {"tabs": [{"sessionID": "ses_other", "title": "`+canary+`"}]},
	  "cwd": {"/w/api": {"tabs": [{"sessionID": "ses_a", "title": "`+canary+`"}]}}}`)
	source := &Source{
		Processes: fakeProcesses{{PID: 7, Dir: "/w/api", StartedAt: now.Add(-time.Hour)}},
		Store:     SQLiteStore{Path: path},
		Tabs:      tabs,
	}

	sessions, err := source.Sessions(context.Background(), now)

	if err != nil || len(sessions) != 1 || sessions[0].Status != agent.StatusWorking {
		t.Fatalf("sessions = %+v, %v; want the tab read from its history, working", sessions, err)
	}
	detected, err := json.Marshal(sessions)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := protocol.Encode(protocol.NewState(agent.Arrange(sessions), time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	for what, data := range map[string][]byte{"sessions": detected, "frame": frame} {
		if bytes.Contains(data, []byte(canary)) {
			t.Errorf("a tab title leaked into the %s: %s", what, data)
		}
	}
}
