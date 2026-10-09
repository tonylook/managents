// Package device finds managents displays on USB serial ports and sends them
// protocol lines.
package device

import (
	"io"
	"runtime"
	"sort"
	"strings"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// BaudRate is fixed by the protocol (115200 8N1).
const BaudRate = 115200

// Port is the part of a serial port the package needs.
type Port interface {
	io.ReadWriteCloser
	SetReadTimeout(time.Duration) error
}

// Opener opens serial ports by name.
type Opener interface {
	Open(name string) (Port, error)
}

// Enumerator lists serial ports that may have a display behind them.
type Enumerator interface {
	Candidates() ([]string, error)
}

// SerialOpener opens real serial ports.
type SerialOpener struct{}

// Open implements Opener. DTR and RTS stay low: on ESP32 boards they drive the
// auto-reset circuit, and asserting them would reboot the display.
func (SerialOpener) Open(name string) (Port, error) {
	return serial.Open(name, &serial.Mode{
		BaudRate:          BaudRate,
		DataBits:          8,
		Parity:            serial.NoParity,
		StopBits:          serial.OneStopBit,
		InitialStatusBits: &serial.ModemOutputBits{DTR: false, RTS: false},
	})
}

// knownBridges are USB VIDs of the serial bridges found on ESP32 display
// boards. Ports behind them are probed first.
var knownBridges = map[string]string{
	"1A86": "WCH CH340/CH9102",
	"10C4": "Silicon Labs CP210x",
	"303A": "Espressif native USB",
	"0403": "FTDI",
}

// USBEnumerator lists USB serial ports, known ESP32 bridges first.
type USBEnumerator struct{}

// Candidates implements Enumerator.
func (USBEnumerator) Candidates() ([]string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	var known, other []string
	for _, p := range ports {
		if !p.IsUSB || !isCallOutDevice(p.Name) {
			continue
		}
		if _, ok := knownBridges[strings.ToUpper(p.VID)]; ok {
			known = append(known, p.Name)
		} else {
			other = append(other, p.Name)
		}
	}
	sort.Strings(known)
	sort.Strings(other)
	return append(known, other...), nil
}

// isCallOutDevice skips macOS /dev/tty.* dial-in nodes, which block on open
// until carrier detect; every device also has a /dev/cu.* twin.
func isCallOutDevice(name string) bool {
	return runtime.GOOS != "darwin" || strings.HasPrefix(name, "/dev/cu.")
}
