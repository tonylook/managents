// Command managents streams the status of local AI coding agents (Claude Code,
// OpenCode) to a managents USB display.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `managents — AI agent status on a USB desk display

Usage:
  managents [run]  [flags]   stream agent status to every connected display (default)
  managents status [flags]   print the sessions detected on this computer
  managents devices          list serial ports and identify managents displays
  managents demo   [flags]   cycle demo screens on the display
  managents version          print the version

Run "managents <command> -h" for the flags of a command.
`

type command func(ctx context.Context, args []string, stdout io.Writer) error

func main() {
	commands := map[string]command{
		"run":     runCommand,
		"status":  statusCommand,
		"devices": devicesCommand,
		"demo":    demoCommand,
		"version": versionCommand,
	}

	name, args := "run", os.Args[1:]
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		name, args = args[0], args[1:]
	}
	if name == "help" {
		fmt.Print(usage)
		return
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", name, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cmd(ctx, args, os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "managents:", err)
		os.Exit(1)
	}
}

func versionCommand(_ context.Context, _ []string, out io.Writer) error {
	_, err := fmt.Fprintln(out, "managents", version)
	return err
}

// newLogger logs to stderr at the requested level.
func newLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q (use debug, info, warn, error)", level)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})), nil
}
