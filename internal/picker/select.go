package picker

import (
	"context"
	"errors"
	"io"
	"strings"
)

// Choice is one selectable value; Note is shown beside it but not filtered.
type Choice struct {
	Value string
	Note  string
}

// SelectOptions configures the filtered single-choice list used by Select.
type SelectOptions struct {
	Title   string
	Choices []Choice
	// Width and Height describe the terminal; zero disables truncation or scrolling.
	Width  int
	Height int
	Color  bool
}

type selector struct {
	screen
	options SelectOptions
	query   string
	matches []int
	cursor  int
	offset  int
	// column aligns notes after the longest value.
	column int
}

// Select returns the index of the chosen value. Typing filters values by
// case-insensitive substring; only listed values can be chosen.
func Select(ctx context.Context, in io.Reader, out io.Writer, options SelectOptions) (int, error) {
	if len(options.Choices) == 0 {
		return 0, errors.New("picker requires choices")
	}
	m := &selector{screen: screen{out: out, width: options.Width, color: options.Color}, options: options}
	for _, choice := range options.Choices {
		m.column = max(m.column, len([]rune(choice.Value)))
	}
	m.filter()
	result := -1
	err := drive(ctx, in, &m.screen, true, m.lines, func(e event) bool {
		switch e.key {
		case keyUp:
			m.cursor = max(m.cursor-1, 0)
		case keyDown:
			m.cursor = max(min(m.cursor+1, len(m.matches)-1), 0)
		case keyText:
			m.query += string(e.char)
			m.filter()
		case keyBackspace:
			if m.query != "" {
				m.query = m.query[:len(m.query)-1]
				m.filter()
			}
		case keyClear:
			m.query = ""
			m.filter()
		case keyEnter:
			if len(m.matches) > 0 {
				result = m.matches[m.cursor]
				return true
			}
		}
		return false
	})
	if err != nil {
		return 0, err
	}
	return result, nil
}

func (m *selector) filter() {
	query := strings.ToLower(m.query)
	m.matches = m.matches[:0]
	for i, choice := range m.options.Choices {
		if strings.Contains(strings.ToLower(choice.Value), query) {
			m.matches = append(m.matches, i)
		}
	}
	m.cursor, m.offset = 0, 0
}

func (m *selector) lines() []string {
	visible := visibleRows(len(m.matches), m.options.Height, 3)
	m.offset = scroll(m.cursor, m.offset, visible)
	lines := []string{
		m.fit(title(m.options.Title, m.cursor, len(m.matches), visible)),
		m.fit("Filter: " + m.query),
	}
	if len(m.matches) == 0 {
		lines = append(lines, m.style("2", m.fit("  No matches")))
	}
	for i := m.offset; i < m.offset+visible && i < len(m.matches); i++ {
		choice := m.options.Choices[m.matches[i]]
		marker := "  "
		if i == m.cursor {
			marker = "> "
		}
		line := marker + choice.Value
		if choice.Note != "" {
			line += strings.Repeat(" ", m.column-len([]rune(choice.Value))+2) + choice.Note
		}
		line = m.fit(line)
		if i == m.cursor {
			line = m.style("1", line)
		}
		lines = append(lines, line)
	}
	return append(lines, m.style("2", m.fit("type to filter · ↑/↓ move · enter select · esc cancel")))
}
