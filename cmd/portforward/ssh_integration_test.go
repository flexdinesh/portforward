//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/ssh"
	"github.com/flexdinesh/portforward/internal/state"
)

func TestRealSSH(t *testing.T) {
	for _, tool := range []string{"ssh", "sshd", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("integration tests require %s: %v", tool, err)
		}
	}
	dir, err := os.MkdirTemp("/tmp", "portforward-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	key, hostKey := filepath.Join(dir, "identity"), filepath.Join(dir, "host-key")
	for _, path := range []string{key, hostKey} {
		command := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("keygen: %v: %s", err, output)
		}
	}
	sshPort, incidentalPort := freePort(t), freePort(t)
	serverConfig := filepath.Join(dir, "sshd_config")
	config := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nStrictModes no\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nAllowTcpForwarding yes\n", sshPort, hostKey, filepath.Join(dir, "sshd.pid"), key+".pub")
	if err := os.WriteFile(serverConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	serverPath, _ := exec.LookPath("sshd")
	log, err := os.Create(filepath.Join(dir, "sshd.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	server := exec.Command(serverPath, "-D", "-e", "-f", serverConfig)
	server.Stdout, server.Stderr = log, log
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", sshPort), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			output, _ := os.ReadFile(filepath.Join(dir, "sshd.log"))
			t.Fatalf("sshd failed to start: %s", output)
		}
		time.Sleep(20 * time.Millisecond)
	}
	hostPublic, err := os.ReadFile(hostKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(fmt.Sprintf("[127.0.0.1]:%d %s", sshPort, hostPublic)), 0o600); err != nil {
		t.Fatal(err)
	}
	clientConfig := filepath.Join(dir, "ssh_config")
	otherSocket := filepath.Join(dir, "unrelated")
	config = fmt.Sprintf("Host testhost\n  HostName 127.0.0.1\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  BatchMode yes\n  StrictHostKeyChecking yes\n  UserKnownHostsFile %s\n  ControlMaster auto\n  ControlPath %s\n  ControlPersist yes\n  LocalForward 127.0.0.1:%d localhost:1\n  RemoteCommand echo should-not-run\n", sshPort, current.Username, key, knownHosts, otherSocket, incidentalPort)
	if err := os.WriteFile(clientConfig, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	realSSH, _ := exec.LookPath("ssh")
	wrapperDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(wrapperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Only fixture config selection is wrapped; every operation uses real OpenSSH.
	wrapper := "#!/bin/sh\nexec " + shellQuote(realSSH) + " -F " + shellQuote(clientConfig) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "ssh"), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	root, err := stateRoot()
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.New(socketDirectory(root), os.Stdin, os.Stdout, os.Stderr)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, _ := store.Read(ctx)
		for _, record := range rows {
			client.Stop(ctx, record.Connection())
		}
		os.RemoveAll(socketDirectory(root))
	})
	executable := filepath.Join(dir, "portforward")
	build := exec.Command("go", "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	invoke := func(wantCode int, arguments ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, arguments...)
		output, err := command.CombinedOutput()
		code := 0
		if err != nil {
			failure, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("CLI: %v: %s", err, output)
			}
			code = failure.ExitCode()
		}
		if code != wantCode {
			t.Fatalf("%v exit=%d, want %d: %s", arguments, code, wantCode, output)
		}
		return string(output)
	}
	list := func() []forward.Entry {
		t.Helper()
		var entries []forward.Entry
		if err := json.Unmarshal([]byte(invoke(0, "list", "--json")), &entries); err != nil {
			t.Fatal(err)
		}
		return entries
	}
	// A real application endpoint proves traffic crosses the SSH connection.
	application, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			conn, err := application.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			io.WriteString(conn, "forwarded\n")
			conn.Close()
		}
	}()
	t.Cleanup(func() { application.Close(); <-finished })
	a, b := freePort(t), freePort(t)
	add := func(port int) string {
		return invoke(0, "add", strconv.Itoa(port), "testhost", "--to", application.Addr().String())
	}
	add(a)
	add(b)
	if output := add(a); !strings.Contains(output, "Already forwarding") {
		t.Fatalf("duplicate: %s", output)
	}
	rows, err := store.Read(context.Background())
	if err != nil || len(rows) != 2 || rows[0].ConnectionID != rows[1].ConnectionID {
		t.Fatalf("shared master: %v %v", rows, err)
	}
	checkTraffic(t, a)
	checkTraffic(t, b)
	// Crash after SSH acceptance, before the final state write. Retrying the
	// same pending add must be accepted by real OpenSSH without rebinding.
	if err := store.WithLock(context.Background(), func(tx forward.Transaction) error {
		pending, err := tx.Load()
		if err != nil {
			return err
		}
		pending[0].Phase = forward.Adding
		return tx.Save(pending)
	}); err != nil {
		t.Fatal(err)
	}
	add(a)
	if entries := list(); len(entries) != 2 || entries[0].Status != "active" || entries[1].Status != "active" {
		t.Fatalf("active list: %+v", entries)
	}
	// Binding a tunnel does not require the remote application to be running.
	c := freePort(t)
	invoke(0, "add", strconv.Itoa(c), "testhost", "--to", fmt.Sprintf("localhost:%d", freePort(t)))
	for _, entry := range list() {
		if entry.LocalPort == c && entry.Status != "active" {
			t.Fatal("application availability must not define tunnel status")
		}
	}
	invoke(0, "remove", strconv.Itoa(c))
	t.Run("IPv6 listener", func(t *testing.T) {
		probe, err := net.Listen("tcp6", "[::1]:0")
		if err != nil {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		port := probe.Addr().(*net.TCPAddr).Port
		probe.Close()
		invoke(0, "add", strconv.Itoa(port), "testhost", "--bind", "::1", "--to", application.Addr().String())
		t.Cleanup(func() { invoke(0, "remove", strconv.Itoa(port), "--bind", "::1") })
		conn, err := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", port), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		output, err := io.ReadAll(conn)
		if err != nil || string(output) != "forwarded\n" {
			t.Fatalf("IPv6 traffic: %q, %v", output, err)
		}
	})
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", incidentalPort), 100*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("configured forward leaked into managed master")
	}
	invoke(1, "remove", strconv.Itoa(a), "wronghost")
	checkTraffic(t, a)
	// An occupied port must remain owned by its original process.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { busy.Close() })
	_, busyPort, _ := net.SplitHostPort(busy.Addr().String())
	invoke(1, "add", busyPort, "testhost", "--to", application.Addr().String())
	if len(list()) != 2 {
		t.Fatal("failed busy-port add must roll back state")
	}
	// Kill a generation, leaving a stale socket. Reconnecting one listener
	// must not revive its sibling or mistake the stale socket for a live master.
	old := rows[0].Connection()
	check := exec.Command("ssh", "-F", "/dev/null", "-S", filepath.Join(socketDirectory(root), old.ID), "-O", "check", "testhost")
	output, err := check.CombinedOutput()
	if err != nil {
		t.Fatalf("check master: %v: %s", err, output)
	}
	match := regexp.MustCompile(`pid=(\d+)`).FindStringSubmatch(string(output))
	if len(match) != 2 {
		t.Fatalf("missing master pid: %s", output)
	}
	pid, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		alive, err := client.Alive(context.Background(), old)
		if !alive && err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("master did not stop: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if entries := list(); entries[0].Status != "disconnected" || entries[1].Status != "disconnected" {
		t.Fatalf("disconnected list: %+v", entries)
	}
	add(a)
	checkTraffic(t, a)
	for _, entry := range list() {
		want := "disconnected"
		if entry.LocalPort == a {
			want = "active"
		}
		if entry.Status != want {
			t.Fatalf("wrong generation status: %+v", entry)
		}
	}
	if output := invoke(0, "reconnect"); !strings.Contains(output, fmt.Sprintf("Reconnected 127.0.0.1:%d", b)) || strings.Contains(output, fmt.Sprintf("Reconnected 127.0.0.1:%d", a)) {
		t.Fatalf("reconnect must restore only the disconnected sibling: %s", output)
	}
	checkTraffic(t, b)
	rows, err = store.Read(context.Background())
	if err != nil || len(rows) != 2 || rows[0].ConnectionID != rows[1].ConnectionID {
		t.Fatalf("reconnected sibling must share the live master: %v %v", rows, err)
	}
	if err := client.Stop(context.Background(), rows[0].Connection()); err != nil {
		t.Fatal(err)
	}
	if output := invoke(0, "reconnect"); strings.Count(output, "Reconnected ") != 2 {
		t.Fatalf("reconnect must restore both disconnected forwards: %s", output)
	}
	checkTraffic(t, a)
	checkTraffic(t, b)
	rows, err = store.Read(context.Background())
	if err != nil || len(rows) != 2 || rows[0].ConnectionID != rows[1].ConnectionID {
		t.Fatalf("batch reconnect must share a master: %v %v", rows, err)
	}
	if err := client.Stop(context.Background(), rows[0].Connection()); err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", a))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { occupied.Close() })
	if output := invoke(1, "reconnect"); !strings.Contains(output, fmt.Sprintf("Reconnected 127.0.0.1:%d", b)) || !strings.Contains(output, fmt.Sprintf("127.0.0.1:%d through testhost:", a)) {
		t.Fatalf("partial reconnect must report success and the busy-port failure: %s", output)
	}
	checkTraffic(t, b)
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	if output := invoke(0, "reconnect"); !strings.Contains(output, fmt.Sprintf("Reconnected 127.0.0.1:%d", a)) {
		t.Fatalf("reconnect must retry the previously occupied port: %s", output)
	}
	checkTraffic(t, a)
	if output := invoke(0, "reconnect"); output != "No disconnected forwards to reconnect.\n" {
		t.Fatalf("active reconnect: %s", output)
	}
	// Independent session on the same alias must survive managed removals.
	independent := exec.Command("ssh", "-f", "-N", "-M", "-S", otherSocket, "-o", "RemoteCommand=none", "-o", "ClearAllForwardings=yes", "-o", "ControlPersist=no", "testhost")
	if output, err := independent.CombinedOutput(); err != nil {
		t.Fatalf("independent SSH: %v %s", err, output)
	}
	t.Cleanup(func() { exec.Command("ssh", "-S", otherSocket, "-O", "exit", "testhost").Run() })
	invoke(0, "remove", strconv.Itoa(a))
	checkTraffic(t, b)
	// Crash after cancellation, before removal commits. Retry must tolerate
	// OpenSSH reporting that this exact forward has already been cancelled.
	rows, err = store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithLock(context.Background(), func(tx forward.Transaction) error {
		pending, err := tx.Load()
		if err != nil {
			return err
		}
		pending[0].Phase = forward.Removing
		return tx.Save(pending)
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel(context.Background(), rows[0].Connection(), rows[0].Mapping); err != nil {
		t.Fatal(err)
	}
	invoke(0, "remove", strconv.Itoa(b), "testhost")
	if output := invoke(0, "remove", strconv.Itoa(b)); !strings.Contains(output, "No matching forward.") {
		t.Fatal(output)
	}
	if output := invoke(0, "list", "--json"); output != "[]\n" {
		t.Fatal(output)
	}
	if output, err := exec.Command("ssh", "-S", otherSocket, "-O", "check", "testhost").CombinedOutput(); err != nil {
		t.Fatalf("unrelated session disrupted: %v: %s", err, output)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func checkTraffic(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	output, err := io.ReadAll(conn)
	if err != nil || string(output) != "forwarded\n" {
		t.Fatalf("traffic: %q, %v", output, err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
