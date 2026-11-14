package picker

import (
	"context"
	"io"
)

// PromptOptions configures the single-line input used by Prompt.
type PromptOptions struct {
	Label string
	// Digits accepts only ASCII digits.
	Digits bool
	// Validate rejects a submitted value; its message is shown until the next edit.
	Validate func(string) error
	// Width describes the terminal; zero disables truncation.
	Width int
	Color bool
}

type prompt struct {
	screen
	options PromptOptions
	input   string
	problem string
}

// Prompt returns the submitted input once Validate accepts it.
func Prompt(ctx context.Context, in io.Reader, out io.Writer, options PromptOptions) (string, error) {
	m := &prompt{screen: screen{out: out, width: options.Width, color: options.Color}, options: options}
	err := drive(ctx, in, &m.screen, true, m.lines, func(e event) bool {
		switch e.key {
		case keyText:
			if !options.Digits || e.char >= '0' && e.char <= '9' {
				m.input += string(e.char)
				m.problem = ""
			}
		case keyBackspace:
			if m.input != "" {
				m.input = m.input[:len(m.input)-1]
				m.problem = ""
			}
		case keyClear:
			m.input, m.problem = "", ""
		case keyEnter:
			if options.Validate == nil {
				return true
			}
			if err := options.Validate(m.input); err != nil {
				m.problem = err.Error()
				return false
			}
			return true
		}
		return false
	})
	if err != nil {
		return "", err
	}
	return m.input, nil
}

func (m *prompt) lines() []string {
	cursor := "_"
	if m.color {
		cursor = "\x1b[7m \x1b[0m"
	}
	// The cursor stays outside fit so styling never splits mid-sequence.
	runes := []rune(m.fit(m.options.Label + " " + m.input + " "))
	input := string(runes[:len(runes)-1]) + cursor
	hint := m.style("2", m.fit("enter confirm · esc cancel"))
	if m.problem != "" {
		hint = m.style("31", m.fit(m.problem))
	}
	return []string{input, hint}
}
