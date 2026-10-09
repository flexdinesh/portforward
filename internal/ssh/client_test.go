package ssh

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/portforward/internal/forward"
)

func TestControlTimeoutRemainsUnknown(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("requires OpenSSH client")
	}
	dir, err := os.MkdirTemp("/tmp", "pf-control-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	connection := forward.Connection{Host: "unused", ID: strings.Repeat("a", 32)}
	listener, err := net.Listen("unix", filepath.Join(dir, connection.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// The socket accepts connections at the OS level but never replies to the
	// mux protocol. A timeout must not classify it as a disconnected master.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	client := New(dir, nil, nil, nil)
	alive, err := client.Alive(ctx, connection)
	if alive || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: alive=%v err=%v", alive, err)
	}
}

func TestStaleSocketIsDisconnected(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("requires OpenSSH client")
	}
	dir, err := os.MkdirTemp("/tmp", "pf-control-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	connection := forward.Connection{Host: "unused", ID: strings.Repeat("b", 32)}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, connection.ID), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	client := New(dir, nil, nil, nil)
	alive, err := client.Alive(context.Background(), connection)
	if alive || err != nil {
		t.Fatalf("stale socket: alive=%v err=%v", alive, err)
	}
	if err := client.Stop(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
}
