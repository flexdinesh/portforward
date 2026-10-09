package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
		{name: "missing remove", args: []string{"remove", "5432", "prod"}, stdout: "No matching forward.\n"},
		{name: "invalid add", args: []string{"add", "0", "prod"}, code: 2, stderr: "invalid port"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), test.args, &stdout, &stderr); code != test.code {
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
		{[]string{"add", "0", "prod"}, 2},
		{[]string{"list"}, 1},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), test.input, &stdout, &stderr); code != test.code {
			t.Fatalf("%v: exit %d, want %d", test.input, code, test.code)
		}
	}
}
