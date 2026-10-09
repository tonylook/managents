package device

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// retryAfter keeps a port that is not a display from being probed (and
// possibly reset) on every discovery pass.
const retryAfter = 30 * time.Second

// Manager keeps the set of connected displays up to date and fans protocol
// lines out to all of them. Displays may come and go at any time.
type Manager struct {
	Enumerator Enumerator
	Opener     Opener
	Logger     *slog.Logger
	// FixedPort, when set, is the only port used (no discovery).
	FixedPort string
	// HandshakeTimeout defaults to DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration

	mu       sync.Mutex
	displays map[string]*Display
	rejected map[string]time.Time
}

// NewManager returns a Manager for the real serial ports of this computer.
func NewManager(logger *slog.Logger, fixedPort string) *Manager {
	return &Manager{Enumerator: USBEnumerator{}, Opener: SerialOpener{}, Logger: logger, FixedPort: fixedPort}
}

// Discover connects to displays that appeared since the last call.
func (m *Manager) Discover(now time.Time) {
	candidates, err := m.candidates()
	if err != nil {
		m.Logger.Warn("listing serial ports failed", "err", err)
		return
	}
	for _, name := range candidates {
		if m.isConnected(name) || m.recentlyRejected(name, now) {
			continue
		}
		display, err := Connect(m.Opener, name, m.handshakeTimeout())
		if err != nil {
			m.Logger.Debug("port skipped", "port", name, "err", err)
			if errors.Is(err, ErrNotADisplay) {
				m.reject(name, now) // a port that is merely busy or gone is retried at once
			}
			continue
		}
		m.Logger.Info("display connected", "port", name, "board", display.Info.Board,
			"firmware", display.Info.FW, "size", sizeOf(display))
		m.add(display)
	}
}

// Broadcast sends one line to every connected display, dropping the ones
// that fail (unplugged); they are picked up again by Discover.
func (m *Manager) Broadcast(line []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, display := range m.displays {
		if err := display.Send(line); err != nil {
			m.Logger.Info("display disconnected", "port", name, "err", err)
			display.Close()
			delete(m.displays, name)
		}
	}
}

// Count is the number of connected displays.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.displays)
}

// Close disconnects every display.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, display := range m.displays {
		display.Close()
		delete(m.displays, name)
	}
}

func (m *Manager) handshakeTimeout() time.Duration {
	if m.HandshakeTimeout > 0 {
		return m.HandshakeTimeout
	}
	return DefaultHandshakeTimeout
}

func (m *Manager) candidates() ([]string, error) {
	if m.FixedPort != "" {
		return []string{m.FixedPort}, nil
	}
	return m.Enumerator.Candidates()
}

func (m *Manager) isConnected(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.displays[name]
	return ok
}

func (m *Manager) recentlyRejected(name string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.rejected[name]
	return ok && now.Sub(at) < retryAfter
}

func (m *Manager) reject(name string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rejected == nil {
		m.rejected = make(map[string]time.Time)
	}
	m.rejected[name] = now
}

func (m *Manager) add(display *Display) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.displays == nil {
		m.displays = make(map[string]*Display)
	}
	m.displays[display.Name] = display
	delete(m.rejected, display.Name)
}

func sizeOf(d *Display) string {
	return fmt.Sprintf("%dx%d", d.Info.W, d.Info.H)
}
