// Package picker renders an inline, keyboard-driven multi-select list on a raw
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

type Options struct {
	Title  string
	Action string
	Rows   []string
	// Width and Height describe the terminal; zero disables truncation or scrolling.
	Width  int
	Height int
	Color  bool
}

type key int

const (
	keyUp key = iota
	keyDown
	keyToggle
	keyEnter
	keyCancel
)

type model struct {
	options  Options
	cursor   int
	offset   int
	selected []bool
	drawn    int
}

// Run returns selected row indices in row order. Without explicit selections,
// enter returns the highlighted row.
func Run(ctx context.Context, in io.Reader, out io.Writer, options Options) ([]int, error) {
	if len(options.Rows) == 0 {
		return nil, errors.New("picker requires rows")
	}
	m := &model{options: options, selected: make([]bool, len(options.Rows))}
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
	if _, err := io.WriteString(out, "\x1b[?25l"); err != nil {
		return nil, err
	}
	result, err := m.loop(ctx, out, chunks, failures)
	return result, errors.Join(err, m.clear(out))
}

func (m *model) loop(ctx context.Context, out io.Writer, chunks <-chan []byte, failures <-chan error) ([]int, error) {
	for {
		if err := m.render(out); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-failures:
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		case chunk := <-chunks:
			for _, k := range parse(chunk) {
				switch k {
				case keyUp:
					m.cursor = max(m.cursor-1, 0)
				case keyDown:
					m.cursor = min(m.cursor+1, len(m.options.Rows)-1)
				case keyToggle:
					m.selected[m.cursor] = !m.selected[m.cursor]
				case keyEnter:
					return m.choice(), nil
				case keyCancel:
					return nil, ErrCancelled
				}
			}
		}
	}
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

func parse(chunk []byte) []key {
	var keys []key
	for i := 0; i < len(chunk); i++ {
		switch chunk[i] {
		case 'k':
			keys = append(keys, keyUp)
		case 'j':
			keys = append(keys, keyDown)
		case ' ':
			keys = append(keys, keyToggle)
		case '\r', '\n':
			keys = append(keys, keyEnter)
		case 'q', 0x03, 0x04:
			keys = append(keys, keyCancel)
		case 0x1b:
			// A lone escape cancels; CSI/SS3 sequences carry arrows or are ignored.
			if i+1 >= len(chunk) || chunk[i+1] != '[' && chunk[i+1] != 'O' {
				keys = append(keys, keyCancel)
				continue
			}
			end := i + 2
			for end < len(chunk) && (chunk[end] < 0x40 || chunk[end] > 0x7e) {
				end++
			}
			if end < len(chunk) {
				switch chunk[end] {
				case 'A':
					keys = append(keys, keyUp)
				case 'B':
					keys = append(keys, keyDown)
				}
			}
			i = end
		}
	}
	return keys
}

func (m *model) visible() int {
	if m.options.Height <= 0 {
		return len(m.options.Rows)
	}
	return max(1, min(len(m.options.Rows), m.options.Height-2))
}

func (m *model) render(out io.Writer) error {
	visible := m.visible()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+visible {
		m.offset = m.cursor - visible + 1
	}
	lines := []string{m.fit(m.title(visible))}
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
	lines = append(lines, m.style("2", m.fit(fmt.Sprintf("↑/↓ move · space select · enter %s %d · esc cancel", m.options.Action, count))))
	var b strings.Builder
	m.rewind(&b)
	b.WriteString(strings.Join(lines, "\r\n"))
	m.drawn = len(lines)
	_, err := io.WriteString(out, b.String())
	return err
}

func (m *model) title(visible int) string {
	if visible == len(m.options.Rows) {
		return m.options.Title
	}
	return fmt.Sprintf("%s (%d/%d)", m.options.Title, m.cursor+1, len(m.options.Rows))
}

// rewind moves to the first drawn line and clears everything below it.
func (m *model) rewind(b *strings.Builder) {
	if m.drawn == 0 {
		return
	}
	b.WriteString("\r")
	if m.drawn > 1 {
		fmt.Fprintf(b, "\x1b[%dA", m.drawn-1)
	}
	b.WriteString("\x1b[J")
}

func (m *model) clear(out io.Writer) error {
	var b strings.Builder
	m.rewind(&b)
	b.WriteString("\x1b[?25h")
	m.drawn = 0
	_, err := io.WriteString(out, b.String())
	return err
}

// fit keeps each line on one terminal row so rewinding stays exact.
func (m *model) fit(line string) string {
	runes := []rune(line)
	if m.options.Width <= 1 || len(runes) < m.options.Width {
		return line
	}
	return string(runes[:m.options.Width-1])
}

func (m *model) style(code, line string) string {
	if !m.options.Color {
		return line
	}
	return "\x1b[" + code + "m" + line + "\x1b[0m"
}
