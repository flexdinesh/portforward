package forward_test

import (
	"testing"

	"github.com/flexdinesh/portforward/internal/forward"
)

func TestParseDestination(t *testing.T) {
	for _, test := range []struct {
		input string
		host  string
		port  int
	}{
		{"db.internal:5432", "db.internal", 5432},
		{"[::1]:5432", "::1", 5432},
		{"[::ffff:10.0.0.1]:80", "10.0.0.1", 80},
	} {
		host, port, err := forward.ParseDestination(test.input)
		if err != nil || host != test.host || port != test.port {
			t.Fatalf("%q: got %q, %d, %v", test.input, host, port, err)
		}
	}
	for _, input := range []string{"", "db", ":5432", "db:0", "db:x", "-db:5432", "db/x:5432", "[fe80::1%eth0]:22"} {
		if host, port, err := forward.ParseDestination(input); err == nil || host != "" || port != 0 {
			t.Fatalf("%q: accepted as %q, %d", input, host, port)
		}
	}
}
