package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tonylook/managents/helper/internal/device"
	"github.com/tonylook/managents/helper/internal/protocol"
	"github.com/tonylook/managents/helper/internal/service"
)

// soundRig is a sender whose serial port and service are fakes.
type soundRig struct {
	*sender
	svc     *fakeService
	sentTo  []string
	sent    [][]byte
	sendErr []error // result of each sendTo call; the last one repeats
	slept   time.Duration
}

func newSoundRig(ports ...string) *soundRig {
	r := &soundRig{svc: &fakeService{}, sendErr: []error{nil}}
	r.sender = &sender{
		ports:   fakePorts{names: ports},
		service: func() (service.Manager, error) { return r.svc, nil },
		sendTo: func(name string, line []byte) error {
			r.sentTo, r.sent = append(r.sentTo, name), append(r.sent, line)
			err := r.sendErr[0]
			if len(r.sendErr) > 1 {
				r.sendErr = r.sendErr[1:]
			}
			return err
		},
		sleep: func(d time.Duration) { r.slept += d },
	}
	return r
}

func (r *soundRig) run(t *testing.T, port string, enabled bool) (string, error) {
	t.Helper()
	var out strings.Builder
	err := r.sender.run(&out, port, enabled)
	return out.String(), err
}

func TestSoundSendsTheSettingsFrame(t *testing.T) {
	r := newSoundRig("/dev/cu.usbserial-10")
	out, err := r.run(t, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.sent) != 1 || r.sentTo[0] != "/dev/cu.usbserial-10" {
		t.Fatalf("sent %v to %v", r.sent, r.sentTo)
	}
	want, _ := protocol.Encode(protocol.NewSettings(true))
	if string(r.sent[0]) != string(want) {
		t.Errorf("sent %q, want %q", r.sent[0], want)
	}
	if !strings.Contains(out, "Turning the chime on") {
		t.Errorf("output: %s", out)
	}

	out, err = r.run(t, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want, _ = protocol.Encode(protocol.NewSettings(false))
	if string(r.sent[1]) != string(want) {
		t.Errorf("sent %q, want %q", r.sent[1], want)
	}
	if !strings.Contains(out, "Turning the chime off") {
		t.Errorf("output: %s", out)
	}
}

func TestSoundPortChoice(t *testing.T) {
	_, err := newSoundRig().run(t, "", true)
	if err == nil || !strings.Contains(err.Error(), "no display found") {
		t.Errorf("no display: %v", err)
	}
	_, err = newSoundRig("/dev/a", "/dev/b").run(t, "", true)
	if err == nil || !strings.Contains(err.Error(), "/dev/a, /dev/b") || !strings.Contains(err.Error(), "--port") {
		t.Errorf("two displays: %v", err)
	}
	r := newSoundRig("/dev/a", "/dev/b")
	if _, err := r.run(t, "/dev/b", true); err != nil || r.sentTo[0] != "/dev/b" {
		t.Errorf("explicit port: %v, %v", err, r.sentTo)
	}
}

func TestSoundStopsAndRestartsTheService(t *testing.T) {
	r := newSoundRig("/dev/a")
	r.svc.running = true
	out, err := r.run(t, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.svc.calls, ","); got != "stop,start" {
		t.Errorf("service calls %q", got)
	}
	if !strings.Contains(out, "Started the managents service again") {
		t.Error(out)
	}
}

func TestSoundRestartsTheServiceAfterAFailure(t *testing.T) {
	r := newSoundRig("/dev/a")
	r.svc.running = true
	r.sendErr = []error{errors.New("cable came out")}
	if _, err := r.run(t, "", true); err == nil {
		t.Fatal("the failure vanished")
	}
	if got := strings.Join(r.svc.calls, ","); got != "stop,start" {
		t.Errorf("service calls %q", got)
	}
}

func TestSoundWaitsForAServiceToLetGoOfThePort(t *testing.T) {
	r := newSoundRig("/dev/a")
	r.svc.running = true
	r.sendErr = []error{device.ErrPortBusy, device.ErrPortBusy, nil}
	if _, err := r.run(t, "", true); err != nil {
		t.Fatal(err)
	}
	if len(r.sent) != 3 || r.slept < 2*time.Second {
		t.Errorf("%d tries, slept %v", len(r.sent), r.slept)
	}
}

func TestSoundBusyPort(t *testing.T) {
	r := newSoundRig("/dev/a")
	r.sendErr = []error{device.ErrPortBusy} // no service running: one try
	_, err := r.run(t, "", true)
	if !errors.Is(err, device.ErrPortBusy) || !strings.Contains(err.Error(), "serial monitor") {
		t.Errorf("got %v", err)
	}
	if len(r.sent) != 1 {
		t.Errorf("%d tries for a port that another program holds", len(r.sent))
	}
}

func TestSoundCommandUsage(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"sound"}, "usage: managents sound on|off"},
		{[]string{"sound", "loudly"}, "sound is on or off"},
		{[]string{"sound", "on", "off"}, "usage: managents sound on|off"},
		{[]string{"sound", "--bogus"}, "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := run(context.Background(), tt.args, &stdout, &stderr)
			if code != exitUsage {
				t.Errorf("exit code %d, want %d", code, exitUsage)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr %q lacks %q", stderr.String(), tt.want)
			}
		})
	}
}
