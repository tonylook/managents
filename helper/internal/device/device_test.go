package device

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

const deviceHello = `{"v":1,"t":"hello","device":"managents","fw":"0.1.0","board":"e32r40t","w":480,"h":320,"proto":1}` + "\n"

// fakePort replays scripted device output, one chunk per Read, and records writes.
type fakePort struct {
	mu       sync.Mutex
	chunks   []string
	written  bytes.Buffer
	writeErr error
	closed   bool
}

func (p *fakePort) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.chunks) == 0 {
		time.Sleep(time.Millisecond) // a read timeout with no data
		return 0, nil
	}
	n := copy(buf, p.chunks[0])
	p.chunks = p.chunks[1:]
	return n, nil
}

func (p *fakePort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	return p.written.Write(data)
}

func (p *fakePort) Close() error                       { p.closed = true; return nil }
func (p *fakePort) SetReadTimeout(time.Duration) error { return nil }

type fakeOpener map[string]*fakePort

func (o fakeOpener) Open(name string) (Port, error) {
	port, ok := o[name]
	if !ok {
		return nil, errors.New("no such port")
	}
	return port, nil
}

type fakeEnumerator []string

func (e fakeEnumerator) Candidates() ([]string, error) { return e, nil }

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHandshakeSkipsBootNoise(t *testing.T) {
	port := &fakePort{chunks: []string{"ets Jun  8 2016 00:22:57\r\n", "rst:0x1 (POWER", "ON_RESET)\n", deviceHello[:30], deviceHello[30:]}}

	hello, err := handshake(port, time.Second)

	if err != nil {
		t.Fatal(err)
	}
	if hello.Board != "e32r40t" || hello.W != 480 {
		t.Errorf("hello = %+v", hello)
	}
	if !strings.HasPrefix(port.written.String(), `{"v":1,"t":"hello"}`) {
		t.Errorf("probe = %q", port.written.String())
	}
}

func TestHandshakeTimesOutOnSilentPort(t *testing.T) {
	_, err := handshake(&fakePort{chunks: []string{"some other device\n"}}, 50*time.Millisecond)
	if !errors.Is(err, ErrNotADisplay) {
		t.Errorf("err = %v, want ErrNotADisplay", err)
	}
}

func TestManagerConnectsBroadcastsAndDropsDisplays(t *testing.T) {
	display := &fakePort{chunks: []string{deviceHello}}
	other := &fakePort{}
	manager := &Manager{
		Enumerator: fakeEnumerator{"/dev/display", "/dev/other", "/dev/missing"},
		Opener:     fakeOpener{"/dev/display": display, "/dev/other": other},
		Logger:     quietLogger(),
		// Keep tests fast: a silent port gives up quickly.
		HandshakeTimeout: 50 * time.Millisecond,
	}
	start := time.Now()

	manager.Discover(start)
	if manager.Count() != 1 {
		t.Fatalf("connected = %d, want 1", manager.Count())
	}
	if !other.closed {
		t.Error("a port without a display must be closed")
	}

	display.written.Reset()
	manager.Broadcast([]byte("frame\n"))
	if display.written.String() != "frame\n" {
		t.Errorf("display got %q", display.written.String())
	}

	display.writeErr = errors.New("device not configured")
	manager.Broadcast([]byte("frame\n"))
	if manager.Count() != 0 || !display.closed {
		t.Error("an unplugged display must be dropped and closed")
	}
}

func TestManagerDoesNotReprobeRejectedPorts(t *testing.T) {
	other := &fakePort{}
	manager := &Manager{
		Enumerator: fakeEnumerator{"/dev/other"},
		Opener:     fakeOpener{"/dev/other": other},
		Logger:     quietLogger(),
		// Keep tests fast: a silent port gives up quickly.
		HandshakeTimeout: 50 * time.Millisecond,
	}
	start := time.Now()
	manager.Discover(start)
	other.written.Reset()

	manager.Discover(start.Add(time.Second))
	if other.written.Len() != 0 {
		t.Error("rejected port was probed again before retryAfter")
	}
}

func TestManagerFixedPort(t *testing.T) {
	display := &fakePort{chunks: []string{deviceHello}}
	manager := &Manager{
		Enumerator: fakeEnumerator{"/dev/ignored"},
		Opener:     fakeOpener{"/dev/fixed": display},
		Logger:     quietLogger(),
		FixedPort:  "/dev/fixed",
	}
	manager.Discover(time.Now())
	if manager.Count() != 1 {
		t.Errorf("connected = %d, want 1", manager.Count())
	}
}
