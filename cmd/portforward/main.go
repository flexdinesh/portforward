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
	"syscall"
	"text/tabwriter"

	"github.com/flexdinesh/portforward/internal/args"
	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/ssh"
	"github.com/flexdinesh/portforward/internal/state"
	"github.com/flexdinesh/portforward/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, input []string, stdout, stderr io.Writer) int {
	options, err := args.Parse(input)
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
	if err := execute(ctx, options, manager, stdout, stderr); err != nil {
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

func execute(ctx context.Context, options args.Options, manager *forward.Manager, stdout, stderr io.Writer) error {
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
		changed, err := manager.Add(ctx, options.Mapping)
		if err != nil {
			return err
		}
		verb := "Added"
		if !changed {
			verb = "Already forwarding"
		}
		_, err = fmt.Fprintf(stdout, "%s %s → %s through %s.\n", verb, options.Mapping.Local(), options.Mapping.Destination(), options.Host)
		return err
	case "remove":
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
