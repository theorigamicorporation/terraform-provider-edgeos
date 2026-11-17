// Copyright (c) The Origami Corporation
// SPDX-License-Identifier: MPL-2.0

// Package configtree models the EdgeOS configuration tree: paths, leaf values,
// flattened leaf sets and the SET/DELETE batches sent to the device.
//
// It has no knowledge of HTTP or Terraform so the conversion rules can be
// tested on their own.
package configtree

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Path is the list of node names from the root of the config tree, for
// example {"firewall", "name", "WAN_IN", "rule", "10"}.
type Path []string

// NewPath returns a path made of the given node names.
func NewPath(names ...string) Path {
	return append(Path(nil), names...)
}

// Child returns a new path with the given names appended. The receiver is
// never modified.
func (p Path) Child(names ...string) Path {
	out := make(Path, 0, len(p)+len(names))
	out = append(out, p...)
	return append(out, names...)
}

// Parent returns the path without its last element. The parent of the root
// is the root.
func (p Path) Parent() Path {
	if len(p) == 0 {
		return Path{}
	}
	return NewPath(p[:len(p)-1]...)
}

// Last returns the last node name, or "" for the root.
func (p Path) Last() string {
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

// Equal reports whether both paths have the same node names.
func (p Path) Equal(q Path) bool {
	return slices.Equal(p, q)
}

// HasPrefix reports whether q is a prefix of p.
func (p Path) HasPrefix(q Path) bool {
	if len(q) > len(p) {
		return false
	}
	return p[:len(q)].Equal(q)
}

// Validate checks that the path can be sent to EdgeOS. Node names must be
// non-empty and must not contain control characters.
func (p Path) Validate() error {
	if len(p) == 0 {
		return errors.New("path must not be empty")
	}
	for i, n := range p {
		if n == "" {
			return fmt.Errorf("path element %d is empty", i)
		}
		if !utf8.ValidString(n) {
			return fmt.Errorf("path element %d is not valid UTF-8", i)
		}
		for _, r := range n {
			if r < 0x20 || r == 0x7f {
				return fmt.Errorf("path element %d (%q) contains a control character", i, n)
			}
		}
	}
	return nil
}

// key is an unambiguous map key for the path.
func (p Path) key() string {
	return strings.Join(p, "\x00")
}

func pathFromKey(k string) Path {
	if k == "" {
		return Path{}
	}
	return Path(strings.Split(k, "\x00"))
}

// String renders the path the way the EdgeOS CLI shows it, quoting elements
// that contain spaces, quotes, backslashes or other special characters.
// ParsePath reverses it.
func (p Path) String() string {
	var b strings.Builder
	for i, n := range p {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quoteElement(n))
	}
	return b.String()
}

func isBareRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("-_./:@+=,%", r)
}

func quoteElement(s string) string {
	bare := s != ""
	for _, r := range s {
		if !isBareRune(r) {
			bare = false
			break
		}
	}
	if bare {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ParsePath parses the CLI form produced by Path.String. Elements are
// separated by whitespace. Double quoted elements support the escapes \" \\
// \n and \t; single quoted elements are taken literally, as in the EdgeOS CLI.
func ParsePath(s string) (Path, error) {
	var (
		out    Path
		cur    strings.Builder
		inElem bool
		rs     = []rune(s)
	)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case ' ', '\t', '\n', '\r':
			if inElem {
				out = append(out, cur.String())
				cur.Reset()
				inElem = false
			}
		case '"':
			inElem = true
			closed := false
			for i++; i < len(rs); i++ {
				c := rs[i]
				if c == '"' {
					closed = true
					break
				}
				if c == '\\' {
					if i+1 >= len(rs) {
						return nil, fmt.Errorf("unfinished escape at end of %q", s)
					}
					i++
					switch rs[i] {
					case 'n':
						cur.WriteByte('\n')
					case 't':
						cur.WriteByte('\t')
					case '"', '\\':
						cur.WriteRune(rs[i])
					default:
						return nil, fmt.Errorf("unknown escape \\%c in %q", rs[i], s)
					}
					continue
				}
				cur.WriteRune(c)
			}
			if !closed {
				return nil, fmt.Errorf("missing closing double quote in %q", s)
			}
		case '\'':
			inElem = true
			closed := false
			for i++; i < len(rs); i++ {
				if rs[i] == '\'' {
					closed = true
					break
				}
				cur.WriteRune(rs[i])
			}
			if !closed {
				return nil, fmt.Errorf("missing closing single quote in %q", s)
			}
		default:
			inElem = true
			cur.WriteRune(r)
		}
	}
	if inElem {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return nil, errors.New("path must not be empty")
	}
	return out, nil
}
