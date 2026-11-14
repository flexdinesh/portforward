package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/picker"
	"github.com/flexdinesh/portforward/internal/state"
)

func TestRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{name: "no args", stdout: "Usage:"},
		{name: "help", args: []string{"--help"}, stdout: "Usage:"},
		{name: "help command", args: []string{"help"}, stdout: "Usage:"},
		{name: "short help", args: []string{"-h"}, stdout: "Usage:"},
		{name: "version", args: []string{"--version"}, stdout: "portforward "},
		{name: "version command", args: []string{"version"}, stdout: "portforward "},
		{name: "short version", args: []string{"-v"}, stdout: "portforward "},
		{name: "unknown", args: []string{"unknown"}, code: 2, stderr: "invalid command"},
		{name: "extra version args", args: []string{"--version", "extra"}, code: 2, stderr: "invalid command"},
		{name: "empty list", args: []string{"list"}, stdout: "No managed forwards.\n"},
		{name: "empty JSON", args: []string{"list", "--json"}, stdout: "[]\n"},
		{name: "empty reconnect", args: []string{"reconnect"}, stdout: "No disconnected forwards to reconnect.\n"},
		{name: "reconnect help", args: []string{"reconnect", "--help"}, stdout: "portforward reconnect"},
		{name: "invalid reconnect", args: []string{"reconnect", "5432"}, code: 2, stderr: "usage: portforward reconnect"},
		{name: "missing remove", args: []string{"remove", "5432", "prod"}, stdout: "No matching forward.\n"},
		{name: "interactive remove without terminal", args: []string{"remove"}, code: 2, stderr: "usage: portforward remove <port> [<host>]"},
		{name: "remove bind without port", args: []string{"remove", "--bind", "::1"}, code: 2, stderr: "usage: portforward remove"},
		{name: "invalid add", args: []string{"add", "prod", "0"}, code: 2, stderr: "invalid port"},
		{name: "old add order", args: []string{"add", "5432", "prod"}, code: 2, stderr: `invalid port "prod"`},
		{name: "interactive add without terminal", args: []string{"add"}, code: 2, stderr: "usage: portforward add <host> <port>"},
		{name: "interactive add host without terminal", args: []string{"add", "prod"}, code: 2, stderr: "usage: portforward add <host> <port>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), test.args, &stdout, &stderr, nil); code != test.code {
				t.Fatalf("exit = %d, want %d", code, test.code)
			}
			for _, stream := range []struct{ got, want string }{
				{stdout.String(), test.stdout},
				{stderr.String(), test.stderr},
			} {
				if stream.want == "" && stream.got != "" || !strings.Contains(stream.got, stream.want) {
					t.Errorf("output = %q, want %q", stream.got, stream.want)
				}
			}
		})
	}
}

func TestConfigurationErrorsDoNotChangeDestination(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "relative/path")
	for _, test := range []struct {
		input []string
		code  int
	}{
		{[]string{"--help"}, 0},
		{[]string{"--version"}, 0},
		{[]string{"add", "prod", "0"}, 2},
		{[]string{"list"}, 1},
		{[]string{"reconnect"}, 1},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), test.input, &stdout, &stderr, nil); code != test.code {
			t.Fatalf("%v: exit %d, want %d", test.input, code, test.code)
		}
	}
}

// seed stores records whose SSH masters are absent, so removal needs no SSH.
func seed(t *testing.T, records ...forward.Record) (*state.Store, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", home)
	root := filepath.Join(home, "portforward")
	store, err := state.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) > 0 {
		if err := store.WithLock(context.Background(), func(tx forward.Transaction) error { return tx.Save(records) }); err != nil {
			t.Fatal(err)
		}
	}
	return store, root
}

func record(t *testing.T, bind string, port int, host, id string) forward.Record {
	t.Helper()
	mapping, err := forward.NewMapping(bind, port, host, "")
	if err != nil {
		t.Fatal(err)
	}
	return forward.Record{Mapping: mapping, ConnectionID: strings.Repeat(id, 32), Phase: forward.Active}
}

func remaining(t *testing.T, store *state.Store) []string {
	t.Helper()
	records, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var locals []string
	for _, r := range records {
		locals = append(locals, r.Local())
	}
	return locals
}

func TestInteractiveRemove(t *testing.T) {
	store, _ := seed(t,
		record(t, "127.0.0.1", 5432, "prod", "a"),
		record(t, "127.0.0.1", 6379, "cache", "b"),
		record(t, "::1", 8080, "dev", "c"),
	)
	var rows []string
	choose := func(_ context.Context, got []string) ([]int, error) {
		rows = got
		return []int{0, 2}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"remove"}, &stdout, &stderr, &ui{removeForwards: choose}); code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr.String())
	}
	if len(rows) != 3 || !strings.HasPrefix(rows[0], "127.0.0.1:5432  prod   localhost:5432  disconnected") {
		t.Fatalf("rows = %q", rows)
	}
	if got := stdout.String(); got != "Removed 127.0.0.1:5432.\nRemoved [::1]:8080.\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := remaining(t, store); !slices.Equal(got, []string{"127.0.0.1:6379"}) {
		t.Fatalf("remaining = %v", got)
	}
}

func TestInteractiveRemoveEmpty(t *testing.T) {
	seed(t)
	choose := func(context.Context, []string) ([]int, error) {
		t.Fatal("picker opened without forwards")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"remove"}, &stdout, &stderr, &ui{removeForwards: choose}); code != 0 || stdout.String() != "No managed forwards.\n" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestInteractiveRemoveCancel(t *testing.T) {
	store, _ := seed(t, record(t, "127.0.0.1", 5432, "prod", "a"))
	choose := func(context.Context, []string) ([]int, error) { return nil, picker.ErrCancelled }
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"remove"}, &stdout, &stderr, &ui{removeForwards: choose}); code != 130 || stderr.String() != "Cancelled.\n" || stdout.Len() != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if got := remaining(t, store); len(got) != 1 {
		t.Fatalf("remaining = %v", got)
	}
}

func TestInteractiveRemoveContinuesAfterFailure(t *testing.T) {
	broken := record(t, "127.0.0.1", 5432, "prod", "a")
	store, root := seed(t, broken, record(t, "127.0.0.1", 6379, "cache", "b"))
	// A regular file in place of the control socket makes removal fail.
	directory := socketDirectory(root)
	t.Cleanup(func() { os.RemoveAll(directory) })
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, broken.ConnectionID), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	choose := func(context.Context, []string) ([]int, error) { return []int{0, 1}, nil }
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"remove"}, &stdout, &stderr, &ui{removeForwards: choose}); code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if stdout.String() != "Removed 127.0.0.1:6379.\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "portforward: 127.0.0.1:5432: invalid SSH control socket") || !strings.HasSuffix(stderr.String(), "portforward: 1 of 2 removals failed\n") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := remaining(t, store); !slices.Equal(got, []string{"127.0.0.1:5432"}) {
		t.Fatalf("remaining = %v", got)
	}
}

func TestInteractiveRemoveChooserError(t *testing.T) {
	seed(t, record(t, "127.0.0.1", 5432, "prod", "a"))
	choose := func(context.Context, []string) ([]int, error) { return nil, errors.New("terminal gone") }
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"remove"}, &stdout, &stderr, &ui{removeForwards: choose}); code != 1 || stderr.String() != "portforward: terminal gone\n" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

// sshHome points HOME at a directory whose SSH config holds config.
func sshHome(t *testing.T, config string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if config == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveAdd(t *testing.T) {
	store, _ := seed(t,
		record(t, "127.0.0.1", 5432, "prod", "a"),
		record(t, "127.0.0.1", 6379, "cache", "b"),
		record(t, "::1", 8080, "prod", "c"),
	)
	sshHome(t, "Host cache bastion *.internal\nHost bad;host\nHost user@jump\n")
	var choices []picker.Choice
	var prompted string
	terminal := &ui{
		selectHost: func(_ context.Context, got []picker.Choice) (int, error) {
			choices = got
			return 2, nil
		},
		readPort: func(_ context.Context, host string) (int, error) {
			prompted = host
			return 6379, nil
		},
	}
	var stdout, stderr bytes.Buffer
	// The chosen listener conflicts, so the mapping reaches add without SSH.
	if code := run(context.Background(), []string{"add"}, &stdout, &stderr, terminal); code != 1 {
		t.Fatalf("exit = %d: %s", code, stderr.String())
	}
	want := []picker.Choice{{Value: "prod", Note: "forward"}, {Value: "cache", Note: "forward"}, {Value: "bastion", Note: "ssh config"}, {Value: "user@jump", Note: "ssh config"}}
	if !slices.Equal(choices, want) {
		t.Fatalf("choices = %v", choices)
	}
	if prompted != "bastion" {
		t.Fatalf("prompted host = %q", prompted)
	}
	if got := stderr.String(); got != "portforward: 127.0.0.1:6379 already maps to localhost:6379 through cache; remove it first\n" {
		t.Fatalf("stderr = %q", got)
	}
	if got := remaining(t, store); len(got) != 3 {
		t.Fatalf("remaining = %v", got)
	}
}

func TestInteractiveAddWithHost(t *testing.T) {
	seed(t, record(t, "::1", 5432, "prod", "a"))
	sshHome(t, "Host prod\n")
	var prompted string
	terminal := &ui{
		selectHost: func(context.Context, []picker.Choice) (int, error) {
			t.Fatal("host picker opened for an explicit host")
			return 0, nil
		},
		readPort: func(_ context.Context, host string) (int, error) {
			prompted = host
			return 5432, nil
		},
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"add", "other", "--bind", "::1", "--to", "db:5432"}, &stdout, &stderr, terminal)
	if code != 1 || prompted != "other" || !strings.Contains(stderr.String(), "[::1]:5432 already maps to localhost:5432 through prod") {
		t.Fatalf("exit = %d, prompted = %q, stderr = %q", code, prompted, stderr.String())
	}
}

func TestInteractiveAddCancel(t *testing.T) {
	seed(t)
	sshHome(t, "Host prod\n")
	for name, terminal := range map[string]*ui{
		"host": {selectHost: func(context.Context, []picker.Choice) (int, error) { return 0, picker.ErrCancelled }},
		"port": {
			selectHost: func(context.Context, []picker.Choice) (int, error) { return 0, nil },
			readPort:   func(context.Context, string) (int, error) { return 0, picker.ErrCancelled },
		},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"add"}, &stdout, &stderr, terminal); code != 130 || stderr.String() != "Cancelled.\n" || stdout.Len() != 0 {
			t.Fatalf("%s: exit = %d, stdout = %q, stderr = %q", name, code, stdout.String(), stderr.String())
		}
	}
}

func TestInteractiveAddWithoutHosts(t *testing.T) {
	seed(t)
	sshHome(t, "Host *\n")
	terminal := &ui{selectHost: func(context.Context, []picker.Choice) (int, error) {
		t.Fatal("host picker opened without hosts")
		return 0, nil
	}}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"add"}, &stdout, &stderr, terminal); code != 1 || stderr.String() != "portforward: no SSH hosts found; run portforward add <host> <port>\n" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestInteractiveAddUnreadableConfig(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads files regardless of mode")
	}
	seed(t)
	sshHome(t, "Host prod\n")
	if err := os.Chmod(filepath.Join(os.Getenv("HOME"), ".ssh", "config"), 0); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"add"}, &stdout, &stderr, &ui{}); code != 1 || !strings.HasPrefix(stderr.String(), "portforward: read SSH config: ") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
