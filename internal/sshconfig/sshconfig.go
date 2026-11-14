// Package sshconfig lists concrete host aliases from the user's OpenSSH
// configuration. It only offers choices; ssh remains the authority on how each
// alias resolves, so Match blocks and conditional includes are not evaluated.
package sshconfig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxDepth matches OpenSSH's include nesting limit and bounds include cycles.
const maxDepth = 16

// Hosts returns Host names without patterns from home/.ssh/config and its
// includes, in file order without duplicates. A missing config has no hosts;
// unreadable includes are skipped.
func Hosts(home string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := parser{home: home, seen: make(map[string]bool)}
	p.parse(data, 0)
	return p.hosts, nil
}

type parser struct {
	home  string
	hosts []string
	seen  map[string]bool
}

func (p *parser) parse(data []byte, depth int) {
	for _, line := range strings.Split(string(data), "\n") {
		keyword, arguments := split(line)
		switch strings.ToLower(keyword) {
		case "host":
			for _, name := range arguments {
				if !strings.ContainsAny(name, "*?!") && !p.seen[name] {
					p.seen[name] = true
					p.hosts = append(p.hosts, name)
				}
			}
		case "include":
			if depth < maxDepth {
				for _, pattern := range arguments {
					p.include(pattern, depth+1)
				}
			}
		}
	}
}

func (p *parser) include(pattern string, depth int) {
	if strings.HasPrefix(pattern, "~/") {
		pattern = filepath.Join(p.home, pattern[2:])
	} else if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(p.home, ".ssh", pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	slices.Sort(matches)
	for _, match := range matches {
		if data, err := os.ReadFile(match); err == nil {
			p.parse(data, depth)
		}
	}
}

// split returns a line's keyword and arguments. Keywords end at whitespace or
// "="; arguments may be double-quoted, and an unquoted "#" starts a comment.
func split(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil
	}
	end := strings.IndexAny(line, " \t=")
	if end < 0 {
		return line, nil
	}
	rest := strings.TrimLeft(line[end:], " \t")
	rest = strings.TrimPrefix(rest, "=")
	var arguments []string
	var current strings.Builder
	quoted, started := false, false
	for _, c := range rest {
		switch {
		case c == '"':
			quoted, started = !quoted, true
		case !quoted && (c == ' ' || c == '\t'):
			if started {
				arguments = append(arguments, current.String())
				current.Reset()
				started = false
			}
		case !quoted && !started && c == '#':
			return line[:end], arguments
		default:
			current.WriteRune(c)
			started = true
		}
	}
	if started {
		arguments = append(arguments, current.String())
	}
	return line[:end], arguments
}
