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

var choices = []Choice{{"prod", "forward"}, {"prod-db", "ssh config"}, {"bastion", "ssh config"}, {"Queue", ""}}

func selectFrom(t *testing.T, input io.Reader, options SelectOptions) (int, string, error) {
	t.Helper()
	if options.Choices == nil {
		options.Choices = choices
	}
	options.Title = "Choose SSH host"
	var out bytes.Buffer
	got, err := Select(context.Background(), input, &out, options)
	return got, out.String(), err
}

func TestSelect(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  int
	}{
		{"enter takes first row", "\r", 0},
		{"arrows move", "\x1b[B\x1b[B\x1b[A\r", 1},
		{"cursor clamps", "\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B\r", 3},
		{"filter by substring", "db\r", 1},
		{"filter ignores case", "QUE\r", 3},
		{"filter resets cursor", "\x1b[B\x1b[Bpro\r", 0},
		{"vim and quit keys are text", "q\r", 3},
		{"backspace widens filter", "bastx\x7f\r", 2},
		{"ctrl-u clears filter", "zzz\x15\x1b[B\r", 1},
		{"enter without matches waits", "zzz\r\x7f\x7f\x7fbas\r", 2},
		{"notes are not filtered", "config\r\x15\r", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := selectFrom(t, strings.NewReader(test.input), SelectOptions{})
			if err != nil || got != test.want {
				t.Fatalf("got %d, %v; want %d", got, err, test.want)
			}
		})
	}
}

func TestSelectCancel(t *testing.T) {
	for _, input := range []string{"\x1b", "\x03", "\x04", "pro\x1bx"} {
		if _, _, err := selectFrom(t, strings.NewReader(input), SelectOptions{}); !errors.Is(err, ErrCancelled) {
			t.Fatalf("%q: err = %v", input, err)
		}
	}
	if _, _, err := selectFrom(t, strings.NewReader("p"), SelectOptions{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
}

func TestSelectRender(t *testing.T) {
	_, out, err := selectFrom(t, keys("p", "r", "o", "\x1b[B", "\r"), SelectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(out, "\x1b[J")
	want := "Choose SSH host\r\n" +
		"Filter: pro\r\n" +
		"  prod     forward\r\n" +
		"> prod-db  ssh config\r\n" +
		"type to filter · ↑/↓ move · enter select · esc cancel\r\x1b[4A"
	if len(frames) < 3 || frames[len(frames)-2] != want {
		t.Fatalf("frames = %q", frames)
	}
	if !strings.HasPrefix(out, "\x1b[?25l") || !strings.HasSuffix(out, "\x1b[J\x1b[?25h") {
		t.Fatalf("cursor not hidden and restored: %q", out)
	}
	_, out, err = selectFrom(t, iotest.OneByteReader(strings.NewReader("zz\x1b")), SelectOptions{})
	if !errors.Is(err, ErrCancelled) || !strings.Contains(out, "Filter: zz\r\n  No matches\r\n") {
		t.Fatalf("no-match frame missing: %v, %q", err, out)
	}
}

func TestSelectScrolling(t *testing.T) {
	_, out, err := selectFrom(t, keys("\x1b[B", "\x1b[B", "\r"), SelectOptions{Height: 5})
	if err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(out, "\x1b[J")
	last := frames[len(frames)-2]
	if !strings.HasPrefix(last, "Choose SSH host (3/4)\r\nFilter: \r\n  prod-db  ssh config\r\n> bastion  ssh config\r\n") {
		t.Fatalf("last frame = %q", last)
	}
}

func runPrompt(t *testing.T, input io.Reader, options PromptOptions) (string, string, error) {
	t.Helper()
	options.Label = "Local port for prod:"
	var out bytes.Buffer
	got, err := Prompt(context.Background(), input, &out, options)
	return got, out.String(), err
}

func validPort(value string) error {
	if value == "" || len(value) > 5 {
		return errors.New("invalid port")
	}
	return nil
}

func TestPrompt(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{"digits", "5432\r", "5432"},
		{"non-digits ignored", "5a4 3x2\r", "5432"},
		{"backspace", "54321\x7f\r", "5432"},
		{"ctrl-u clears", "99\x155432\r", "5432"},
		{"invalid then retry", "\r123456\r\x7f\r", "12345"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := runPrompt(t, strings.NewReader(test.input), PromptOptions{Digits: true, Validate: validPort})
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
	if got, _, err := runPrompt(t, strings.NewReader("a b\r"), PromptOptions{}); err != nil || got != "a b" {
		t.Fatalf("text prompt: %q, %v", got, err)
	}
}

func TestPromptCancel(t *testing.T) {
	for _, input := range []string{"\x1b", "54\x03", "\x04"} {
		if _, _, err := runPrompt(t, strings.NewReader(input), PromptOptions{Digits: true}); !errors.Is(err, ErrCancelled) {
			t.Fatalf("%q: err = %v", input, err)
		}
	}
}

func TestPromptRender(t *testing.T) {
	_, out, err := runPrompt(t, iotest.OneByteReader(strings.NewReader("5\r\x7f6\r")), PromptOptions{Digits: true, Validate: func(value string) error {
		if value != "6" {
			return errors.New("invalid port")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(out, "\x1b[J")
	for _, want := range []string{
		"Local port for prod: _\r\nenter confirm · esc cancel\r\x1b[1A",
		"Local port for prod: 5_\r\ninvalid port\r\x1b[1A",
		"Local port for prod: _\r\nenter confirm · esc cancel\r\x1b[1A",
		"Local port for prod: 6_\r\nenter confirm · esc cancel\r\x1b[1A",
	} {
		if !slices.Contains(frames, want) {
			t.Fatalf("missing frame %q in %q", want, frames)
		}
	}
	if !strings.HasSuffix(out, "\x1b[J\x1b[?25h") {
		t.Fatalf("prompt not cleared: %q", out)
	}
	_, out, err = runPrompt(t, strings.NewReader("5\r"), PromptOptions{Color: true, Width: 12})
	if err != nil || !strings.Contains(out, "Local port\x1b[7m \x1b[0m") {
		t.Fatalf("styled prompt: %v, %q", err, out)
	}
}

// keyReader delivers one key per read, as terminals deliver escape sequences.
type keyReader struct{ keys []string }

func keys(keys ...string) *keyReader { return &keyReader{keys: keys} }

func (r *keyReader) Read(buffer []byte) (int, error) {
	if len(r.keys) == 0 {
		return 0, io.EOF
	}
	n := copy(buffer, r.keys[0])
	r.keys = r.keys[1:]
	return n, nil
}
