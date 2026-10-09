package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/protocol"
)

type fakeSource struct {
	name     string
	sessions []agent.Session
	err      error
}

func (s *fakeSource) Name() string { return s.name }
func (s *fakeSource) Sessions(context.Context, time.Time) ([]agent.Session, error) {
	return s.sessions, s.err
}

type fakeDisplays struct {
	connected   int
	discoveries int
	frames      []protocol.State
}

func (d *fakeDisplays) Discover(time.Time) { d.discoveries++ }
func (d *fakeDisplays) Count() int         { return d.connected }
func (d *fakeDisplays) Broadcast(line []byte) {
	var state protocol.State
	if err := json.Unmarshal(line, &state); err != nil {
		panic(err)
	}
	d.frames = append(d.frames, state)
}

type harness struct {
	now      time.Time
	source   *fakeSource
	displays *fakeDisplays
	runner   *Runner
}

func newHarness() *harness {
	h := &harness{
		now:      time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
		source:   &fakeSource{name: "fake"},
		displays: &fakeDisplays{connected: 1},
	}
	h.runner = NewRunner([]detect.Source{h.source}, h.displays, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.runner.Clock = func() time.Time { return h.now }
	return h
}

func (h *harness) step(advance time.Duration) {
	h.now = h.now.Add(advance)
	h.runner.Step(context.Background())
}

func working(id string) agent.Session {
	return agent.Session{ID: id, Kind: agent.KindClaude, Dir: "/w/" + id, Status: agent.StatusWorking}
}

func TestSendsImmediatelyThenKeepAlive(t *testing.T) {
	h := newHarness()
	h.source.sessions = []agent.Session{working("a")}

	h.step(0)
	h.step(time.Second)
	if len(h.displays.frames) != 1 {
		t.Fatalf("frames after 1s = %d, want 1 (no change, keep-alive not due)", len(h.displays.frames))
	}
	h.step(time.Second)
	if len(h.displays.frames) != 2 {
		t.Fatalf("frames after 2s = %d, want 2 (keep-alive)", len(h.displays.frames))
	}
}

func TestSendsOnChangeWithoutWaiting(t *testing.T) {
	h := newHarness()
	h.source.sessions = []agent.Session{working("a")}
	h.step(0)

	h.source.sessions = []agent.Session{working("a"), working("b")}
	h.step(time.Second)

	if len(h.displays.frames) != 2 || len(h.displays.frames[1].Agents) != 2 {
		t.Fatalf("frames = %+v, want a second frame with two agents", h.displays.frames)
	}
}

func TestNoFramesWithoutDisplays(t *testing.T) {
	h := newHarness()
	h.displays.connected = 0
	h.step(0)
	if len(h.displays.frames) != 0 || h.displays.discoveries != 1 {
		t.Errorf("frames=%d discoveries=%d", len(h.displays.frames), h.displays.discoveries)
	}
}

func TestNewDisplayGetsFrameAtOnce(t *testing.T) {
	h := newHarness()
	h.step(0)
	h.displays.connected = 0
	h.step(time.Second)
	h.displays.connected = 1
	h.step(500 * time.Millisecond)
	if len(h.displays.frames) != 2 {
		t.Errorf("frames = %d, want 2", len(h.displays.frames))
	}
}

func TestFailingSourceStillSendsTheRest(t *testing.T) {
	h := newHarness()
	broken := &fakeSource{name: "broken", err: errors.New("boom")}
	h.runner.Sources = append(h.runner.Sources, broken)
	h.source.sessions = []agent.Session{working("a")}

	h.step(0)

	if len(h.displays.frames) != 1 || len(h.displays.frames[0].Agents) != 1 {
		t.Errorf("frames = %+v", h.displays.frames)
	}
}

func TestFingerprintIgnoresClockAndAges(t *testing.T) {
	base := protocol.State{V: 1, T: "state", Now: 100, Agents: []protocol.AgentEntry{{ID: "a", Status: "working", Age: 1}}}
	later := base
	later.Now = 200
	later.Agents = []protocol.AgentEntry{{ID: "a", Status: "working", Age: 101}}
	changed := base
	changed.Agents = []protocol.AgentEntry{{ID: "a", Status: "waiting", Age: 1}}

	if Fingerprint(base) != Fingerprint(later) {
		t.Error("clock/age ticks must not count as a change")
	}
	if Fingerprint(base) == Fingerprint(changed) {
		t.Error("a status change must change the fingerprint")
	}
	if base.Agents[0].Age != 1 {
		t.Error("Fingerprint modified its input")
	}
}
