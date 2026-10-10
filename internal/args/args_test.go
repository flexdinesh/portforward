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
		{"add 5432 prod", "127.0.0.1:5432", "localhost:5432"},
		{"add 15432 user@bastion --to db.internal:5432", "127.0.0.1:15432", "db.internal:5432"},
		{"add --bind ::1 15432 prod --to=[::1]:5432", "[::1]:15432", "[::1]:5432"},
		{"add 5432 --bind=::ffff:127.0.0.1 prod", "127.0.0.1:5432", "localhost:5432"},
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
		"add", "add 5432", "add 5432 prod extra", "add 0 prod", "add 65536 prod",
		"add +5432 prod", "add 5432 -prod", "add 5432 prod --to :5432",
		"add 5432 prod --to db:0", "add 5432 prod --to db", "add 5432 prod --to",
		"add 5432 prod --bind localhost", "add 5432 prod --bind ::1 --bind 127.0.0.1",
		"add 5432 prod --json", "remove", "remove 5432 prod extra", "remove 5432 --to db:5432",
		"list extra", "list --json=true", "list --json --json", "list --bind 127.0.0.1",
		"--version extra", "add 5432 prod --to=", "add 5432 prod --unknown",
		"reconnect 5432", "reconnect --json", "reconnect --bind ::1", "reconnect --to db:5432",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(strings.Fields(input)); err == nil {
				t.Fatal("expected argument error")
			}
		})
	}
	for _, host := range []string{"prod\nother", "user@host;evil", "$(evil)", "", "-oProxyCommand=evil"} {
		if _, err := Parse([]string{"add", "5432", host}); err == nil {
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
