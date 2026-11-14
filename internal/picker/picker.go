// Package picker renders inline, keyboard-driven terminal controls on a raw
// terminal. Callers own terminal modes; the picker only reads keys and writes
// ANSI output, so tests can drive it with plain byte streams.
package picker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrCancelled = errors.New("cancelled")

// Options configures the multi-select list used by Run.
type Options struct {
	Title  string
	Action string
	Rows   []string
	// Width and Height describe the terminal; zero disables truncation or scrolling.
	Width  int
	Height int
	Color  bool
}

type model struct {
	screen
	options  Options
	cursor   int
	offset   int
	selected []bool
}

// Run returns selected row indices in row order. Without explicit selections,
// enter returns the highlighted row.
func Run(ctx context.Context, in io.Reader, out io.Writer, options Options) ([]int, error) {
	if len(options.Rows) == 0 {
		return nil, errors.New("picker requires rows")
	}
	m := &model{
		screen:   screen{out: out, width: options.Width, color: options.Color},
		options:  options,
		selected: make([]bool, len(options.Rows)),
	}
	var result []int
	err := drive(ctx, in, &m.screen, false, m.lines, func(e event) bool {
		switch e.key {
		case keyUp:
			m.cursor = max(m.cursor-1, 0)
		case keyDown:
			m.cursor = min(m.cursor+1, len(m.options.Rows)-1)
		case keyToggle:
			m.selected[m.cursor] = !m.selected[m.cursor]
		case keyEnter:
			result = m.choice()
			return true
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (m *model) choice() []int {
	var result []int
	for i, selected := range m.selected {
		if selected {
			result = append(result, i)
		}
	}
	if len(result) == 0 {
		return []int{m.cursor}
	}
	return result
}

func (m *model) lines() []string {
	visible := visibleRows(len(m.options.Rows), m.options.Height, 2)
	m.offset = scroll(m.cursor, m.offset, visible)
	lines := []string{m.fit(title(m.options.Title, m.cursor, len(m.options.Rows), visible))}
	for i := m.offset; i < m.offset+visible; i++ {
		marker, box := "  ", "[ ] "
		if i == m.cursor {
			marker = "> "
		}
		if m.selected[i] {
			box = "[x] "
		}
		line := m.fit(marker + box + m.options.Rows[i])
		if i == m.cursor {
			line = m.style("1", line)
		}
		lines = append(lines, line)
	}
	count := len(m.choice())
	return append(lines, m.style("2", m.fit(fmt.Sprintf("↑/↓ move · space select · enter %s %d · esc cancel", m.options.Action, count))))
}

type key int

const (
	keyUp key = iota
	keyDown
	keyToggle
	keyEnter
	keyCancel
	keyText
	keyBackspace
	keyClear
)

type event struct {
	key  key
	char byte
}

// drive hides the cursor and renders until handle reports completion or the
// input, context, or user cancels. The control is always cleared afterwards.
func drive(ctx context.Context, in io.Reader, s *screen, text bool, render func() []string, handle func(event) bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunks := make(chan []byte)
	failures := make(chan error, 1)
	// The reader may stay blocked in Read after return; callers exit soon after.
	go func() {
		for {
			buffer := make([]byte, 64)
			n, err := in.Read(buffer)
			if n > 0 {
				select {
				case chunks <- buffer[:n]:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				failures <- err
				return
			}
		}
	}()
	if _, err := io.WriteString(s.out, "\x1b[?25l"); err != nil {
		return err
	}
	err := loop(ctx, s, text, render, handle, chunks, failures)
	return errors.Join(err, s.clear())
}

func loop(ctx context.Context, s *screen, text bool, render func() []string, handle func(event) bool, chunks <-chan []byte, failures <-chan error) error {
	for {
		if err := s.draw(render()); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		case chunk := <-chunks:
			for _, e := range parse(chunk, text) {
				if e.key == keyCancel {
					return ErrCancelled
				}
				if handle(e) {
					return nil
				}
			}
		}
	}
}

// parse decodes keys. In text mode printable ASCII is input, so only arrows,
// escape, and control keys navigate or cancel.
func parse(chunk []byte, text bool) []event {
	var events []event
	add := func(k key) { events = append(events, event{key: k}) }
	for i := 0; i < len(chunk); i++ {
		c := chunk[i]
		switch {
		case c == '\r' || c == '\n':
			add(keyEnter)
		case c == 0x03 || c == 0x04:
			add(keyCancel)
		case c == 0x1b:
			// A lone escape cancels; CSI/SS3 sequences carry arrows or are ignored.
			if i+1 >= len(chunk) || chunk[i+1] != '[' && chunk[i+1] != 'O' {
				add(keyCancel)
				continue
			}
			end := i + 2
			for end < len(chunk) && (chunk[end] < 0x40 || chunk[end] > 0x7e) {
				end++
			}
			if end < len(chunk) {
				switch chunk[end] {
				case 'A':
					add(keyUp)
				case 'B':
					add(keyDown)
				}
			}
			i = end
		case text && (c == 0x7f || c == 0x08):
			add(keyBackspace)
		case text && c == 0x15:
			add(keyClear)
		case text && c >= 0x20 && c <= 0x7e:
			events = append(events, event{key: keyText, char: c})
		case !text && c == 'k':
			add(keyUp)
		case !text && c == 'j':
			add(keyDown)
		case !text && c == ' ':
			add(keyToggle)
		case !text && c == 'q':
			add(keyCancel)
		}
	}
	return events
}

// visibleRows reserves chrome lines for titles, inputs, and hints.
func visibleRows(rows, height, chrome int) int {
	if height <= 0 {
		return rows
	}
	return max(1, min(rows, height-chrome))
}

func scroll(cursor, offset, visible int) int {
	if cursor < offset {
		return cursor
	}
	if cursor >= offset+visible {
		return cursor - visible + 1
	}
	return offset
}

func title(text string, cursor, rows, visible int) string {
	if visible >= rows {
		return text
	}
	return fmt.Sprintf("%s (%d/%d)", text, cursor+1, rows)
}

type screen struct {
	out   io.Writer
	width int
	color bool
	drawn int
}

func (s *screen) draw(lines []string) error {
	var b strings.Builder
	s.rewind(&b)
	b.WriteString(strings.Join(lines, "\r\n"))
	s.drawn = len(lines)
	_, err := io.WriteString(s.out, b.String())
	return err
}

// rewind moves to the first drawn line and clears everything below it.
func (s *screen) rewind(b *strings.Builder) {
	if s.drawn == 0 {
		return
	}
	b.WriteString("\r")
	if s.drawn > 1 {
		fmt.Fprintf(b, "\x1b[%dA", s.drawn-1)
	}
	b.WriteString("\x1b[J")
}

func (s *screen) clear() error {
	var b strings.Builder
	s.rewind(&b)
	b.WriteString("\x1b[?25h")
	s.drawn = 0
	_, err := io.WriteString(s.out, b.String())
	return err
}

// fit keeps each line on one terminal row so rewinding stays exact.
func (s *screen) fit(line string) string {
	runes := []rune(line)
	if s.width <= 1 || len(runes) < s.width {
		return line
	}
	return string(runes[:s.width-1])
}

func (s *screen) style(code, line string) string {
	if !s.color {
		return line
	}
	return "\x1b[" + code + "m" + line + "\x1b[0m"
}
