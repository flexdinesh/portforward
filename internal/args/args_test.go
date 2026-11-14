package args

import (
	"strings"
	"testing"
)

func TestMappings(t *testing.T) {
	for _, test := range []struct {
		input       string
		local       string
		destination string
	}{
		{"add prod 5432", "127.0.0.1:5432", "localhost:5432"},
		{"add user@bastion 15432 --to db.internal:5432", "127.0.0.1:15432", "db.internal:5432"},
		{"add --bind ::1 prod 15432 --to=[::1]:5432", "[::1]:15432", "[::1]:5432"},
		{"add prod --bind=::ffff:127.0.0.1 5432", "127.0.0.1:5432", "localhost:5432"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := Parse(strings.Fields(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if got.Mapping.Local() != test.local || got.Mapping.Destination() != test.destination {
				t.Fatalf("unexpected mapping: %+v", got.Mapping)
			}
		})
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, input := range []string{
		"add 5432 prod", "add prod 5432 extra", "add prod 0", "add prod 65536",
		"add prod +5432", "add -prod 5432", "add prod 5432 --to :5432",
		"add prod 5432 --to db:0", "add prod 5432 --to db", "add prod 5432 --to",
		"add prod 5432 --bind localhost", "add prod 5432 --bind ::1 --bind 127.0.0.1",
		"add prod 5432 --json", "add --bind localhost", "add prod --to db", "add --to=db/x:5432",
		"add $(evil)", "remove --bind ::1", "remove 5432 prod extra", "remove 5432 --to db:5432",
		"list extra", "list --json=true", "list --json --json", "list --bind 127.0.0.1",
		"--version extra", "add prod 5432 --to=", "add prod 5432 --unknown",
		"reconnect 5432", "reconnect --json", "reconnect --bind ::1", "reconnect --to db:5432",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(strings.Fields(input)); err == nil {
				t.Fatal("expected argument error")
			}
		})
	}
	for _, host := range []string{"prod\nother", "user@host;evil", "$(evil)", "", "-oProxyCommand=evil"} {
		if _, err := Parse([]string{"add", host, "5432"}); err == nil {
			t.Fatalf("accepted host %q", host)
		}
	}
}

func TestReconnect(t *testing.T) {
	got, err := Parse([]string{"reconnect"})
	if err != nil || got.Command != "reconnect" {
		t.Fatalf("reconnect: %+v, %v", got, err)
	}
	got, err = Parse([]string{"reconnect", "--help"})
	if err != nil || got.Command != "help" {
		t.Fatalf("reconnect help: %+v, %v", got, err)
	}
}

func TestRemoveOptionalGuard(t *testing.T) {
	for _, input := range [][]string{{"remove", "5432"}, {"remove", "5432", "prod"}} {
		got, err := Parse(input)
		if err != nil || got.Port != 5432 || got.Bind != "127.0.0.1" {
			t.Fatalf("unexpected remove: %+v, %v", got, err)
		}
		if len(input) == 3 && got.Host != "prod" || len(input) == 2 && got.Host != "" {
			t.Fatal("incorrect host guard")
		}
	}
}

func TestInteractiveRemove(t *testing.T) {
	got, err := Parse([]string{"remove"})
	if err != nil || got.Command != "remove" || !got.Interactive {
		t.Fatalf("interactive remove: %+v, %v", got, err)
	}
	got, err = Parse([]string{"remove", "5432"})
	if err != nil || got.Interactive {
		t.Fatalf("explicit remove became interactive: %+v, %v", got, err)
	}
}

func TestInteractiveAdd(t *testing.T) {
	for _, test := range []struct {
		input       string
		host        string
		bind        string
		destination string
	}{
		{"add", "", "127.0.0.1", ""},
		{"add prod", "prod", "127.0.0.1", ""},
		{"add --bind ::ffff:127.0.0.1 --to db.internal:5432", "", "127.0.0.1", "db.internal:5432"},
		{"add user@bastion --to=[::1]:5432", "user@bastion", "127.0.0.1", "[::1]:5432"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := Parse(strings.Fields(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if got.Command != "add" || !got.Interactive || got.Host != test.host || got.Bind != test.bind || got.Destination != test.destination {
				t.Fatalf("unexpected options: %+v", got)
			}
		})
	}
	got, err := Parse([]string{"add", "prod", "5432"})
	if err != nil || got.Interactive || got.Host != "prod" || got.Port != 5432 {
		t.Fatalf("explicit add became interactive: %+v, %v", got, err)
	}
}
