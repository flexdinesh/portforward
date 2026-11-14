package args

import (
	"fmt"
	"strings"

	"github.com/flexdinesh/portforward/internal/forward"
)

const Usage = `portforward — manage local SSH port forwards

Usage:
  portforward list [--json]
  portforward reconnect
  portforward add [<host> [<port>]] [--to <host:port>] [--bind <address>]
  portforward remove [<port> [<host>]] [--bind <address>]
  portforward --help
  portforward --version

Defaults: bind 127.0.0.1; destination localhost:<port> on the SSH server.
Hosts may be SSH config aliases or [user@]hostname. Only managed tunnels are listed.
Use --to for a different remote host or port; IPv6 destinations use [::1]:5432.
Use reconnect to retry all disconnected forwards with their saved mappings.
Run add without a host or port in a terminal to choose them interactively.
Run remove without arguments in a terminal to choose forwards interactively.
`

type Options struct {
	Command     string
	Interactive bool
	JSON        bool
	Mapping     forward.Mapping
	Bind        string
	Port        int
	Host        string
	// Destination is the raw --to value, kept for interactive add.
	Destination string
}

func Parse(input []string) (Options, error) {
	if len(input) == 0 {
		return Options{Command: "help"}, nil
	}
	command := input[0]
	if len(input) == 1 {
		switch command {
		case "help", "--help", "-h":
			return Options{Command: "help"}, nil
		case "version", "--version", "-v":
			return Options{Command: "version"}, nil
		}
	}
	if command != "list" && command != "reconnect" && command != "add" && command != "remove" {
		return Options{}, fmt.Errorf("invalid command %q; run portforward --help", command)
	}
	if len(input) == 2 && (input[1] == "--help" || input[1] == "-h") {
		return Options{Command: "help"}, nil
	}
	options := Options{Command: command, Bind: forward.DefaultBind}
	var positional []string
	destination := ""
	seen := make(map[string]bool)
	for i := 1; i < len(input); i++ {
		argument := input[i]
		if !strings.HasPrefix(argument, "-") {
			positional = append(positional, argument)
			continue
		}
		flag, value, hasValue := strings.Cut(argument, "=")
		if seen[flag] {
			return Options{}, fmt.Errorf("duplicate flag %s", flag)
		}
		seen[flag] = true
		if flag == "--json" && command == "list" && !hasValue {
			options.JSON = true
			continue
		}
		if !(flag == "--bind" && (command == "add" || command == "remove") || flag == "--to" && command == "add") {
			return Options{}, fmt.Errorf("unsupported flag %s for %s", flag, command)
		}
		if !hasValue {
			i++
			if i >= len(input) || strings.HasPrefix(input[i], "-") {
				return Options{}, fmt.Errorf("%s requires a value", flag)
			}
			value = input[i]
		}
		if value == "" {
			return Options{}, fmt.Errorf("%s requires a value", flag)
		}
		if flag == "--bind" {
			options.Bind = value
		} else {
			destination = value
		}
	}
	if command == "list" {
		if len(positional) != 0 {
			return Options{}, fmt.Errorf("usage: portforward list [--json]")
		}
		return options, nil
	}
	if command == "reconnect" {
		if len(positional) != 0 {
			return Options{}, fmt.Errorf("usage: portforward reconnect")
		}
		return options, nil
	}
	if command == "add" {
		return parseAdd(options, positional, destination)
	}
	if command == "remove" && len(positional) == 0 && !seen["--bind"] {
		return Options{Command: command, Interactive: true}, nil
	}
	if command == "remove" && (len(positional) < 1 || len(positional) > 2) {
		return Options{}, fmt.Errorf("usage: portforward remove <port> [<host>]")
	}
	var err error
	options.Port, err = forward.ParsePort(positional[0])
	if err != nil {
		return Options{}, err
	}
	options.Bind, err = forward.NormalizeBind(options.Bind)
	if err != nil {
		return Options{}, err
	}
	if len(positional) == 2 {
		options.Host = positional[1]
		if err := forward.ValidateSSHHost(options.Host); err != nil {
			return Options{}, err
		}
	}
	return options, nil
}

// parseAdd accepts <host> <port>; a missing port or host selects interactive add.
func parseAdd(options Options, positional []string, destination string) (Options, error) {
	if len(positional) > 2 {
		return Options{}, fmt.Errorf("usage: portforward add <host> <port>")
	}
	var err error
	options.Bind, err = forward.NormalizeBind(options.Bind)
	if err != nil {
		return Options{}, err
	}
	if destination != "" {
		if _, _, err := forward.ParseDestination(destination); err != nil {
			return Options{}, err
		}
	}
	options.Destination = destination
	if len(positional) > 0 {
		options.Host = positional[0]
		if err := forward.ValidateSSHHost(options.Host); err != nil {
			return Options{}, err
		}
	}
	if len(positional) < 2 {
		options.Interactive = true
		return options, nil
	}
	options.Port, err = forward.ParsePort(positional[1])
	if err != nil {
		return Options{}, err
	}
	options.Mapping, err = forward.NewMapping(options.Bind, options.Port, options.Host, destination)
	return options, err
}
