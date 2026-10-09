package ssh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/privatefs"
)

type Client struct {
	directory  string
	input      *os.File
	output     *os.File
	diagnostic *os.File
}

func New(directory string, input, output, diagnostic *os.File) *Client {
	return &Client{directory: directory, input: input, output: output, diagnostic: diagnostic}
}

func (c *Client) socket(connection forward.Connection) (string, error) {
	// Validate persisted IDs before using them as filenames.
	if err := connection.Validate(); err != nil {
		return "", err
	}
	path := filepath.Join(c.directory, connection.ID)
	if len(path) > 100 {
		return "", fmt.Errorf("SSH control socket path exceeds 100 bytes")
	}
	return path, nil
}

func (c *Client) Alive(ctx context.Context, connection forward.Connection) (bool, error) {
	if err := privatefs.Dir(c.directory, false); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	path, err := c.socket(connection)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return false, fmt.Errorf("invalid SSH control socket %s", path)
	}
	_, err = c.control(ctx, connection, "check", nil)
	if err == nil {
		return true, nil
	}
	// A stale Unix socket is normal after a killed master. Classify only
	// known local connect failures; timeouts and protocol failures stay unknown.
	var failure *controlError
	if errors.As(err, &failure) && failure.connectFailed() {
		return false, nil
	}
	return false, err
}

func (c *Client) Start(ctx context.Context, connection forward.Connection) error {
	if err := privatefs.Dir(c.directory, true); err != nil {
		return err
	}
	path, err := c.socket(connection)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("refusing to replace an existing SSH control socket %s", path)
	}
	args := []string{"-f", "-N", "-T", "-M", "-S", path}
	for _, option := range []string{
		"ControlMaster=yes", "ControlPersist=no", "ClearAllForwardings=yes",
		"ExitOnForwardFailure=yes", "ServerAliveInterval=15", "ServerAliveCountMax=3",
		"ConnectTimeout=15", "ConnectionAttempts=1", "ForwardAgent=no", "ForwardX11=no",
		"PermitLocalCommand=no", "RemoteCommand=none", "RequestTTY=no", "SessionType=none", "Tunnel=no",
	} {
		args = append(args, "-o", option)
	}
	args = append(args, "--", connection.Host)
	command := exec.CommandContext(ctx, "ssh", args...)
	// Real file descriptors let the detached master outlive the CLI without
	// retaining os/exec copy pipes or blocking Wait. SSH owns interactive prompts.
	command.Stdin, command.Stdout, command.Stderr = c.input, c.output, c.diagnostic
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("start SSH connection to %s: %w", connection.Host, err)
	}
	alive, err := c.Alive(ctx, connection)
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("SSH master did not start for %s", connection.Host)
	}
	return nil
}

func (c *Client) Forward(ctx context.Context, connection forward.Connection, mapping forward.Mapping) error {
	_, err := c.control(ctx, connection, "forward", &mapping)
	return err
}

func (c *Client) Cancel(ctx context.Context, connection forward.Connection, mapping forward.Mapping) error {
	alive, err := c.Alive(ctx, connection)
	if err != nil || !alive {
		return err
	}
	_, err = c.control(ctx, connection, "cancel", &mapping)
	var failure *controlError
	if errors.As(err, &failure) && (failure.connectFailed() || strings.Contains(failure.output, "forwarding request failed: port not forwarded")) {
		return nil
	}
	return err
}

func (c *Client) Stop(ctx context.Context, connection forward.Connection) error {
	alive, err := c.Alive(ctx, connection)
	if err != nil || !alive {
		return err
	}
	_, err = c.control(ctx, connection, "exit", nil)
	var failure *controlError
	if errors.As(err, &failure) && failure.connectFailed() {
		return nil
	}
	return err
}

type controlError struct {
	operation string
	output    string
	err       error
}

func (e *controlError) Error() string {
	return fmt.Sprintf("SSH %s: %s (%v)", e.operation, e.output, e.err)
}

func (e *controlError) Unwrap() error { return e.err }

func (e *controlError) connectFailed() bool {
	return strings.Contains(e.output, "Control socket connect(") &&
		(strings.Contains(e.output, "No such file or directory") || strings.Contains(e.output, "Connection refused"))
}

func (c *Client) control(ctx context.Context, connection forward.Connection, operation string, mapping *forward.Mapping) (string, error) {
	path, err := c.socket(connection)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Control commands do not need host resolution. Ignoring SSH config here
	// prevents configured forwards from leaking into -O forward/cancel requests.
	args := []string{"-F", "/dev/null", "-S", path, "-O", operation}
	if mapping != nil {
		if err := mapping.Validate(); err != nil {
			return "", err
		}
		if connection.Host != mapping.SSHHost {
			return "", fmt.Errorf("SSH connection host does not match mapping")
		}
		args = append(args, "-L", mapping.Local()+":"+mapping.Destination())
	}
	args = append(args, "--", connection.Host)
	command := exec.CommandContext(ctx, "ssh", args...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", &controlError{operation: operation, output: strings.TrimSpace(string(output)), err: err}
	}
	return string(output), nil
}
