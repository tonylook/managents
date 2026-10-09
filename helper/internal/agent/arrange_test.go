package agent

import (
	"reflect"
	"testing"
	"time"
)

func TestDisplayNames(t *testing.T) {
	tests := []struct {
		name string
		dirs []string
		want []string
	}{
		{"base name", []string{"/home/ann/agent-lights"}, []string{"agent-lights"}},
		{"trailing slash", []string{"/home/ann/core/"}, []string{"core"}},
		{
			"same base, different folders",
			[]string{"/work/a/core", "/work/b/core"},
			[]string{"a/core", "b/core"},
		},
		{
			"same folder twice",
			[]string{"/work/api", "/work/api"},
			[]string{"api #1", "api #2"},
		},
		{
			"same folder twice plus a namesake",
			[]string{"/x/api", "/x/api", "/y/api"},
			[]string{"x/api #1", "x/api #2", "y/api"},
		},
		{"root", []string{"/"}, []string{"/"}},
		{"empty", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayNames(tt.dirs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DisplayNames(%q) = %q, want %q", tt.dirs, got, tt.want)
			}
		})
	}
}

func TestArrangeOrdersByActivity(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	session := func(id string, status Status, started, changed time.Duration) Session {
		return Session{ID: id, Dir: "/w/" + id, Status: status, StartedAt: now.Add(-started), Since: now.Add(-changed)}
	}
	sessions := []Session{
		session("idle-for-hours", StatusIdle, 5*time.Hour, 3*time.Hour),
		session("waiting-long", StatusWaiting, 4*time.Hour, 50*time.Minute),
		session("working-newer", StatusWorking, time.Hour, time.Second),
		session("waiting-recent", StatusWaiting, 3*time.Hour, 2*time.Minute),
		session("working-older", StatusWorking, 2*time.Hour, 20*time.Minute),
		session("error-recent", StatusError, 6*time.Hour, 30*time.Second),
	}

	var got []string
	for _, card := range Arrange(sessions) {
		got = append(got, card.ID)
	}

	want := []string{"working-older", "working-newer", "error-recent", "waiting-recent", "waiting-long", "idle-for-hours"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v\nwant    %v", got, want)
	}
	if sessions[0].ID != "idle-for-hours" {
		t.Error("Arrange modified its input")
	}
}

func TestArrangeNames(t *testing.T) {
	cards := Arrange([]Session{{ID: "claude:1", Dir: "/w/first"}})
	if cards[0].Name != "first" {
		t.Errorf("name = %q, want %q", cards[0].Name, "first")
	}
}

func TestSessionAge(t *testing.T) {
	now := time.Now()
	if got := (Session{Since: now.Add(-90 * time.Second)}).Age(now); got != 90*time.Second {
		t.Errorf("Age = %v, want 90s", got)
	}
	if got := (Session{}).Age(now); got != 0 {
		t.Errorf("Age of unknown change time = %v, want 0", got)
	}
	if got := (Session{Since: now.Add(time.Minute)}).Age(now); got != 0 {
		t.Errorf("Age with clock skew = %v, want 0", got)
	}
}
