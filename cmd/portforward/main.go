package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/flexdinesh/portforward/internal/args"
	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/picker"
	"github.com/flexdinesh/portforward/internal/ssh"
	"github.com/flexdinesh/portforward/internal/sshconfig"
	"github.com/flexdinesh/portforward/internal/state"
	"github.com/flexdinesh/portforward/internal/version"
	"golang.org/x/term"
)

// ui holds interactive terminal controls; nil means no interactive terminal.
type ui struct {
	removeForwards func(ctx context.Context, rows []string) ([]int, error)
	selectHost     func(ctx context.Context, choices []picker.Choice) (int, error)
	readPort       func(ctx context.Context, host string) (int, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, terminalUI())
	stop()
	os.Exit(code)
}

func terminalUI() *ui {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return nil
	}
	color := os.Getenv("NO_COLOR") == ""
	// raw runs one control in raw mode. Input stops before the terminal is
	// restored, so no read outlives the control.
	raw := func(control func(input io.Reader, width, height int) error) error {
		width, height, err := term.GetSize(out)
		if err != nil {
			return err
		}
		previous, err := term.MakeRaw(in)
		if err != nil {
			return err
		}
		reader := &ttyReader{fd: in}
		err = control(reader, width, height)
		reader.stop()
		return errors.Join(err, term.Restore(in, previous))
	}
	return &ui{
		removeForwards: func(ctx context.Context, rows []string) ([]int, error) {
			var selected []int
			err := raw(func(input io.Reader, width, height int) error {
				var err error
				selected, err = picker.Run(ctx, input, os.Stdout, picker.Options{
					Title: "Remove forwards", Action: "remove", Rows: rows,
					Width: width, Height: height, Color: color,
				})
				return err
			})
			return selected, err
		},
		selectHost: func(ctx context.Context, choices []picker.Choice) (int, error) {
			var selected int
			err := raw(func(input io.Reader, width, height int) error {
				var err error
				selected, err = picker.Select(ctx, input, os.Stdout, picker.SelectOptions{
					Title: "Choose SSH host", Choices: choices,
					Width: width, Height: height, Color: color,
				})
				return err
			})
			return selected, err
		},
		readPort: func(ctx context.Context, host string) (int, error) {
			var port int
			err := raw(func(input io.Reader, width, _ int) error {
				value, err := picker.Prompt(ctx, input, os.Stdout, picker.PromptOptions{
					Label: "Local port for " + host + ":", Digits: true,
					Validate: func(value string) error {
						_, err := forward.ParsePort(value)
						return err
					},
					Width: width, Color: color,
				})
				if err != nil {
					return err
				}
				port, err = forward.ParsePort(value)
				return err
			})
			return port, err
		},
	}
}

func run(ctx context.Context, input []string, stdout, stderr io.Writer, terminal *ui) int {
	options, err := args.Parse(input)
	if err == nil && options.Interactive && terminal == nil {
		err = errors.New("usage: portforward remove <port> [<host>]")
		if options.Command == "add" {
			err = errors.New("usage: portforward add <host> <port>")
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "portforward: %s\n", err)
		return 2
	}
	switch options.Command {
	case "help":
		fmt.Fprint(stdout, args.Usage)
		return 0
	case "version":
		fmt.Fprintf(stdout, "portforward %s\n", version.String())
		return 0
	}
	root, err := stateRoot()
	if err != nil {
		fmt.Fprintf(stderr, "portforward: %s\n", err)
		return 1
	}
	store, err := state.New(root)
	if err != nil {
		fmt.Fprintf(stderr, "portforward: %s\n", err)
		return 1
	}
	// Short, private paths avoid Unix socket limits even with a long state root.
	client := ssh.New(socketDirectory(root), os.Stdin, os.Stdout, os.Stderr)
	manager := forward.New(store, client)
	err = execute(ctx, options, manager, stdout, stderr, terminal)
	if errors.Is(err, picker.ErrCancelled) {
		fmt.Fprintln(stderr, "Cancelled.")
		return 130
	}
	if err != nil {
		fmt.Fprintf(stderr, "portforward: %s\n", err)
		return 1
	}
	return 0
}

func socketDirectory(root string) string {
	namespace := sha256.Sum256([]byte(root))
	return filepath.Join("/tmp", "portforward-"+strconv.Itoa(os.Getuid())+"-"+fmt.Sprintf("%x", namespace[:8]))
}

func stateRoot() (string, error) {
	if root := os.Getenv("XDG_STATE_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("XDG_STATE_HOME must be absolute")
		}
		return filepath.Join(root, "portforward"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "portforward"), nil
	}
	return filepath.Join(home, ".local", "state", "portforward"), nil
}

func execute(ctx context.Context, options args.Options, manager *forward.Manager, stdout, stderr io.Writer, terminal *ui) error {
	switch options.Command {
	case "list":
		entries, diagnostics, err := manager.List(ctx)
		if err != nil {
			return err
		}
		for _, diagnostic := range diagnostics {
			fmt.Fprintf(stderr, "portforward: %s\n", diagnostic)
		}
		if options.JSON {
			return json.NewEncoder(stdout).Encode(entries)
		}
		if len(entries) == 0 {
			_, err := fmt.Fprintln(stdout, "No managed forwards.")
			return err
		}
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "LOCAL\tSSH HOST\tDESTINATION\tSTATUS")
		for _, entry := range entries {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", entry.Local(), entry.SSHHost, entry.Destination(), entry.Status)
		}
		return writer.Flush()
	case "reconnect":
		restored, err := manager.Reconnect(ctx)
		for _, mapping := range restored {
			if _, writeErr := fmt.Fprintf(stdout, "Reconnected %s → %s through %s.\n", mapping.Local(), mapping.Destination(), mapping.SSHHost); writeErr != nil {
				return errors.Join(err, writeErr)
			}
		}
		if len(restored) == 0 && err == nil {
			_, err = fmt.Fprintln(stdout, "No disconnected forwards to reconnect.")
		}
		return err
	case "add":
		mapping := options.Mapping
		if options.Interactive {
			var err error
			mapping, err = addInteractive(ctx, options, manager, stderr, terminal)
			if err != nil {
				return err
			}
		}
		changed, err := manager.Add(ctx, mapping)
		if err != nil {
			return err
		}
		verb := "Added"
		if !changed {
			verb = "Already forwarding"
		}
		_, err = fmt.Fprintf(stdout, "%s %s → %s through %s.\n", verb, mapping.Local(), mapping.Destination(), mapping.SSHHost)
		return err
	case "remove":
		if options.Interactive {
			return removeInteractive(ctx, manager, stdout, stderr, terminal.removeForwards)
		}
		removed, err := manager.Remove(ctx, options.Bind, options.Port, options.Host)
		if err != nil {
			return err
		}
		if !removed {
			_, err = fmt.Fprintln(stdout, "No matching forward.")
		} else {
			_, err = fmt.Fprintf(stdout, "Removed %s.\n", net.JoinHostPort(options.Bind, strconv.Itoa(options.Port)))
		}
		return err
	default:
		return fmt.Errorf("unsupported command %q", options.Command)
	}
}

// addInteractive chooses the missing host and port, then builds the mapping
// that the normal add path applies.
func addInteractive(ctx context.Context, options args.Options, manager *forward.Manager, stderr io.Writer, terminal *ui) (forward.Mapping, error) {
	host := options.Host
	if host == "" {
		choices, err := hostChoices(ctx, manager, stderr)
		if err != nil {
			return forward.Mapping{}, err
		}
		if len(choices) == 0 {
			return forward.Mapping{}, errors.New("no SSH hosts found; run portforward add <host> <port>")
		}
		index, err := terminal.selectHost(ctx, choices)
		if err != nil {
			return forward.Mapping{}, err
		}
		host = choices[index].Value
	}
	port, err := terminal.readPort(ctx, host)
	if err != nil {
		return forward.Mapping{}, err
	}
	return forward.NewMapping(options.Bind, port, host, options.Destination)
}

// hostChoices lists hosts of managed forwards, then SSH config aliases.
func hostChoices(ctx context.Context, manager *forward.Manager, stderr io.Writer) ([]picker.Choice, error) {
	entries, diagnostics, err := manager.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(stderr, "portforward: %s\n", diagnostic)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	aliases, err := sshconfig.Hosts(home)
	if err != nil {
		return nil, fmt.Errorf("read SSH config: %w", err)
	}
	var choices []picker.Choice
	seen := make(map[string]bool)
	for _, entry := range entries {
		if !seen[entry.SSHHost] {
			seen[entry.SSHHost] = true
			choices = append(choices, picker.Choice{Value: entry.SSHHost, Note: "forward"})
		}
	}
	for _, alias := range aliases {
		if !seen[alias] && forward.ValidateSSHHost(alias) == nil {
			seen[alias] = true
			choices = append(choices, picker.Choice{Value: alias, Note: "ssh config"})
		}
	}
	return choices, nil
}

func removeInteractive(ctx context.Context, manager *forward.Manager, stdout, stderr io.Writer, choose func(context.Context, []string) ([]int, error)) error {
	entries, diagnostics, err := manager.List(ctx)
	if err != nil {
		return err
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(stderr, "portforward: %s\n", diagnostic)
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintln(stdout, "No managed forwards.")
		return err
	}
	var table strings.Builder
	writer := tabwriter.NewWriter(&table, 0, 4, 2, ' ', 0)
	for _, entry := range entries {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", entry.Local(), entry.SSHHost, entry.Destination(), entry.Status)
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	selected, err := choose(ctx, strings.Split(strings.TrimSuffix(table.String(), "\n"), "\n"))
	if err != nil {
		return err
	}
	failed := 0
	for _, index := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := entries[index]
		// The recorded host guards against a listener replaced after listing.
		removed, err := manager.Remove(ctx, entry.BindAddress, entry.LocalPort, entry.SSHHost)
		if err != nil {
			failed++
			fmt.Fprintf(stderr, "portforward: %s: %s\n", entry.Local(), err)
			continue
		}
		message := "Removed " + entry.Local() + "."
		if !removed {
			message = "No matching forward."
		}
		if _, err := fmt.Fprintln(stdout, message); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d removals failed", failed, len(selected))
	}
	return nil
}
