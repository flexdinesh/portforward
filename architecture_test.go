package portforward_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCoreDependencies(t *testing.T) {
	const root = "github.com/flexdinesh/portforward/internal/forward"
	output, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./internal/forward").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect core dependencies: %v: %s", err, output)
	}
	found := false
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == root {
			found = true
			continue
		}
		if strings.HasPrefix(dependency, "github.com/flexdinesh/portforward/") {
			t.Errorf("core transitively imports application adapter %s", dependency)
		}
	}
	if !found {
		t.Fatal("expected lifecycle package disappeared")
	}
}
