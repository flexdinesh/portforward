package picker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

var rows = []string{"127.0.0.1:5432  prod", "127.0.0.1:6379  cache", "[::1]:8080      dev"}

func run(t *testing.T, input string, options Options) ([]int, string, error) {
	t.Helper()
	return runFrom(t, strings.NewReader(input), options)
}

// runBytes delivers one byte per read so each key renders its own frame.
func runBytes(t *testing.T, input string, options Options) ([]int, string, error) {
	t.Helper()
	return runFrom(t, iotest.OneByteReader(strings.NewReader(input)), options)
}

func runFrom(t *testing.T, input io.Reader, options Options) ([]int, string, error) {
	t.Helper()
	if options.Rows == nil {
		options.Rows = rows
	}
	options.Title, options.Action = "Remove forwards", "remove"
	var out bytes.Buffer
	got, err := Run(context.Background(), input, &out, options)
	return got, out.String(), err
}

func TestSelection(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  []int
	}{
		{"enter takes highlighted row", "\r", []int{0}},
		{"down arrow", "\x1b[B\r", []int{1}},
		{"application-mode arrows", "\x1bOB\x1bOB\x1bOA\r", []int{1}},
		{"vim keys", "jjk\n", []int{1}},
		{"cursor clamps", "jjjjj\r", []int{2}},
		{"cursor clamps at top", "kk\x1b[A\r", []int{0}},
		{"checked rows in row order", " jj \r", []int{0, 2}},
		{"checked rows replace cursor", "j k\r", []int{1}},
		{"untoggle falls back to cursor", "  j\r", []int{1}},
		{"ignores unknown sequences", "\x1b[3~x\r", []int{0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := run(t, test.input, Options{})
			if err != nil || !slices.Equal(got, test.want) {
				t.Fatalf("got %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestCancel(t *testing.T) {
	for _, input := range []string{"\x1b", "q", "\x03", "\x04", "j\x1bx"} {
		got, _, err := run(t, input, Options{})
		if !errors.Is(err, ErrCancelled) || got != nil {
			t.Fatalf("%q: got %v, %v", input, got, err)
		}
	}
}

func TestEndOfInput(t *testing.T) {
	if _, _, err := run(t, "j", Options{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	_, err := Run(ctx, reader, io.Discard, Options{Rows: rows})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRender(t *testing.T) {
	_, out, err := runBytes(t, "j \r", Options{})
	if err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(out, "\x1b[J")
	want := "Remove forwards\r\n" +
		"  [ ] 127.0.0.1:5432  prod\r\n" +
		"> [x] 127.0.0.1:6379  cache\r\n" +
		"  [ ] [::1]:8080      dev\r\n" +
		"↑/↓ move · space select · enter remove 1 · esc cancel\r\x1b[4A"
	// Frames: initial, after "j", after " " (rewind trails each frame).
	if len(frames) != 4 || frames[2] != want {
		t.Fatalf("frames = %q", frames)
	}
	if !strings.HasPrefix(out, "\x1b[?25l") || !strings.HasSuffix(out, "\x1b[J\x1b[?25h") {
		t.Fatalf("cursor not hidden and restored: %q", out)
	}
}

func TestRenderStylesAndFits(t *testing.T) {
	_, out, err := run(t, "\r", Options{Color: true, Width: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\x1b[1m> [ ] 127.0\x1b[0m") || !strings.Contains(out, "\x1b[2m↑/↓ move · \x1b[0m") {
		t.Fatalf("output = %q", out)
	}
}

func TestScrolling(t *testing.T) {
	_, out, err := runBytes(t, "jj\r", Options{Height: 4})
	if err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(out, "\x1b[J")
	last := frames[len(frames)-2]
	if !strings.HasPrefix(last, "Remove forwards (3/3)\r\n  [ ] 127.0.0.1:6379  cache\r\n> [ ] [::1]:8080      dev\r\n") {
		t.Fatalf("last frame = %q", last)
	}
}
