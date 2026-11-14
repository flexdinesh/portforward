package sshconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hosts(t *testing.T, home string) []string {
	t.Helper()
	got, err := Hosts(home)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestHosts(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".ssh", "config"), `# comment
Host prod prod-db
  HostName prod.example.com
host=bastion
HOST = "quoted name" spaced
Host *.internal !secret db? *
Host  dev  # trailing comment
Host prod
Match host other
  User someone
Hostname notahost
`)
	want := []string{"prod", "prod-db", "bastion", "quoted name", "spaced", "dev"}
	if got := hosts(t, home); !slices.Equal(got, want) {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
}

func TestIncludes(t *testing.T) {
	home := t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	absolute := filepath.Join(t.TempDir(), "absolute.conf")
	write(t, filepath.Join(ssh, "config"), "Host first\nInclude conf.d/* ~/extra.conf "+absolute+" missing.conf\nHost last\n")
	write(t, filepath.Join(ssh, "conf.d", "b.conf"), "Host b\n")
	write(t, filepath.Join(ssh, "conf.d", "a.conf"), "Host a\nInclude nested.conf\n")
	write(t, filepath.Join(ssh, "nested.conf"), "Host nested\n")
	write(t, filepath.Join(home, "extra.conf"), "Host extra\n")
	write(t, absolute, "Host absolute first\n")
	want := []string{"first", "a", "nested", "b", "extra", "absolute", "last"}
	if got := hosts(t, home); !slices.Equal(got, want) {
		t.Fatalf("hosts = %q, want %q", got, want)
	}
}

func TestIncludeCycleIsBounded(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".ssh", "config"), "Host top\nInclude loop.conf\n")
	write(t, filepath.Join(home, ".ssh", "loop.conf"), "Host loop\nInclude loop.conf\n")
	if got := hosts(t, home); !slices.Equal(got, []string{"top", "loop"}) {
		t.Fatalf("hosts = %q", got)
	}
}

func TestMissingConfig(t *testing.T) {
	if got := hosts(t, t.TempDir()); len(got) != 0 {
		t.Fatalf("hosts = %q", got)
	}
}

func TestUnreadableFiles(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads files regardless of mode")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".ssh", "config")
	write(t, config, "Host prod\nInclude private.conf\n")
	write(t, filepath.Join(home, ".ssh", "private.conf"), "Host hidden\n")
	if err := os.Chmod(filepath.Join(home, ".ssh", "private.conf"), 0); err != nil {
		t.Fatal(err)
	}
	if got := hosts(t, home); !slices.Equal(got, []string{"prod"}) {
		t.Fatalf("hosts = %q", got)
	}
	if err := os.Chmod(config, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Hosts(home); err == nil {
		t.Fatal("unreadable config accepted")
	}
}
