package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tonylook/managents/helper/internal/device"
	"github.com/tonylook/managents/helper/internal/protocol"
	"github.com/tonylook/managents/helper/internal/service"
)

// soundCommand turns the display's status chime on or off. The setting is
// decided on the computer (ADR-0002) and sent as one settings frame, which the
// display stores in its NVS: the command only runs when the choice changes,
// never at every handshake. The service holds the port, so it is stopped for
// the send and started again, as `managents flash` does.
func soundCommand(_ context.Context, inv *invocation) error {
	flags := inv.flags()
	port := portFlag(flags)
	if err := flags.Parse(inv.args); err != nil {
		return errUsage // the flag package printed the error and the usage
	}
	if flags.NArg() != 1 {
		return inv.usageError(flags, "usage: managents sound on|off")
	}
	var enabled bool
	switch strings.ToLower(flags.Arg(0)) {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		return inv.usageError(flags, "sound is on or off, not %q", flags.Arg(0))
	}
	s := &sender{
		ports:   device.USBEnumerator{},
		service: serviceManager,
		sendTo:  sendSettings,
		sleep:   time.Sleep,
	}
	return s.run(inv.stdout, *port, enabled)
}

// sender is what soundCommand does its work with, so tests can replace the
// serial port and the service manager.
type sender struct {
	ports   device.Enumerator
	service func() (service.Manager, error)
	// sendTo connects to the display behind the port and sends it one line.
	sendTo func(name string, line []byte) error
	sleep  func(time.Duration)
}

func (s *sender) run(out io.Writer, portName string, enabled bool) (err error) {
	if portName == "" {
		if portName, err = s.pickPort(); err != nil {
			return err
		}
	}
	line, err := protocol.Encode(protocol.NewSettings(enabled))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Turning the chime %s on %s\n", onOff(enabled), portName)

	stopped, restart := stopServiceForPort(out, s.service, "while sending the setting")
	if restart != nil {
		defer func() { err = errors.Join(err, restart()) }()
	}

	for attempt := 0; ; attempt++ {
		err = s.sendTo(portName, line)
		// A service that was just stopped may need a moment to close the port.
		if !stopped || !errors.Is(err, device.ErrPortBusy) || attempt >= int(portReleaseWait/time.Second) {
			break
		}
		s.sleep(time.Second)
	}
	switch {
	case errors.Is(err, device.ErrPortBusy):
		return fmt.Errorf("%w: close any serial monitor or other program that uses %s, then try again", err, portName)
	case err != nil:
		return err
	}
	fmt.Fprintln(out, "Done: the display remembers the setting and plays the chime once an agent needs you")
	return nil
}

// pickPort returns the port of the only display candidate.
func (s *sender) pickPort() (string, error) {
	ports, err := s.ports.Candidates()
	if err != nil {
		return "", err
	}
	switch len(ports) {
	case 0:
		return "", errors.New("no display found on a USB serial port: plug it in with a data cable, or name the port with --port")
	case 1:
		return ports[0], nil
	}
	return "", fmt.Errorf("several displays found, choose one with --port: %s", strings.Join(ports, ", "))
}

// sendSettings is sender.sendTo for a real serial port: the handshake, the
// line, and the port closed again.
func sendSettings(name string, line []byte) error {
	display, err := device.Connect(device.SerialOpener{}, name, device.DefaultHandshakeTimeout)
	if err != nil {
		return err
	}
	return errors.Join(display.Send(line), display.Close())
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
