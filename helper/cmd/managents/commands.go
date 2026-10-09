package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
	"github.com/tonylook/managents/helper/internal/app"
	"github.com/tonylook/managents/helper/internal/demo"
	"github.com/tonylook/managents/helper/internal/detect"
	"github.com/tonylook/managents/helper/internal/detect/claude"
	"github.com/tonylook/managents/helper/internal/detect/opencode"
	"github.com/tonylook/managents/helper/internal/device"
	"github.com/tonylook/managents/helper/internal/process"
	"github.com/tonylook/managents/helper/internal/protocol"
)

// defaultSources wires the detectors to this computer. claudeWindow forces the
// Claude context limit in tokens (0 = infer).
func defaultSources(claudeWindow int) ([]detect.Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	procs := process.System{}
	claudeSource := claude.NewSource(home, procs)
	claudeSource.ContextWindow = claudeWindow
	return []detect.Source{
		claudeSource,
		&opencode.Source{Processes: procs, Store: opencode.SQLiteStore{Path: opencode.DefaultDatabasePath(home)}},
	}, nil
}

func runCommand(ctx context.Context, args []string, _ io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	port := flags.String("port", "", "use this serial port only, e.g. /dev/cu.usbserial-10 or COM3 (default: auto-detect)")
	logLevel := flags.String("log-level", "info", "debug, info, warn or error")
	claudeWindow := claudeContextFlag(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	logger, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	sources, err := defaultSources(*claudeWindow)
	if err != nil {
		return err
	}

	displays := device.NewManager(logger, *port)
	defer displays.Close()
	logger.Info("managents started", "version", version)
	return app.NewRunner(sources, displays, logger).Run(ctx)
}

func statusCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print the protocol frame that would be sent")
	claudeWindow := claudeContextFlag(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	sources, err := defaultSources(*claudeWindow)
	if err != nil {
		return err
	}

	now := time.Now()
	sessions, detectErr := detect.All(ctx, now, sources)
	cards := agent.Arrange(sessions)
	if *asJSON {
		line, err := protocol.Encode(protocol.NewState(cards, now))
		if err != nil {
			return err
		}
		_, err = out.Write(line)
		return err
	}

	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tKIND\tSTATUS\tAGE\tCONTEXT\tDIRECTORY")
	for _, c := range cards {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Name, c.Kind, c.Status,
			c.Age(now).Truncate(time.Second), contextText(c.Context), c.Dir)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if len(cards) == 0 {
		fmt.Fprintln(out, "no open agent sessions")
	}
	return detectErr
}

func claudeContextFlag(flags *flag.FlagSet) *int {
	return flags.Int("claude-context", 0,
		"Claude context window in tokens, e.g. 1000000 (default: 200k, or 1M once a session exceeds 200k)")
}

func contextText(usage *agent.ContextUsage) string {
	switch {
	case usage == nil:
		return "-"
	case usage.Limit > 0:
		return fmt.Sprintf("%dk/%dk", usage.Used/1000, usage.Limit/1000)
	default:
		return fmt.Sprintf("%dk", usage.Used/1000)
	}
}

func devicesCommand(_ context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("devices", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	ports, err := device.USBEnumerator{}.Candidates()
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		fmt.Fprintln(out, "no USB serial ports found")
		return nil
	}
	for _, name := range ports {
		display, err := device.Connect(device.SerialOpener{}, name, device.DefaultHandshakeTimeout)
		if err != nil {
			fmt.Fprintf(out, "%s\tnot a managents display (%v)\n", name, err)
			continue
		}
		info, _ := json.Marshal(display.Info)
		fmt.Fprintf(out, "%s\tmanagents display %s\n", name, info)
		display.Close()
	}
	return nil
}

func demoCommand(ctx context.Context, args []string, _ io.Writer) error {
	flags := flag.NewFlagSet("demo", flag.ContinueOnError)
	port := flags.String("port", "", "use this serial port only (default: auto-detect)")
	hold := flags.Duration("hold", 6*time.Second, "how long each demo screen stays up")
	if err := flags.Parse(args); err != nil {
		return err
	}
	logger, err := newLogger("info")
	if err != nil {
		return err
	}
	displays := device.NewManager(logger, *port)
	defer displays.Close()

	for i := 0; ; i++ {
		displays.Discover(time.Now())
		scenes := demo.Scenes(time.Now())
		scene := scenes[i%len(scenes)]
		logger.Info("showing", "scene", scene.Title, "displays", displays.Count())
		deadline := time.Now().Add(*hold)
		for time.Now().Before(deadline) {
			line, err := protocol.Encode(protocol.NewState(scene.Cards, time.Now()))
			if err != nil {
				return err
			}
			displays.Broadcast(line)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
		}
	}
}
