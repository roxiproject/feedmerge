package config

import (
	"fmt"
	"strings"
)

// This file implements the YAML subset feedmerge understands: nested mappings,
// sequences of scalars, sequences of mappings, quoted or bare scalars, '#'
// comments and blank lines. Anchors, flow collections, multi-line scalars,
// multiple documents and tags are deliberately not supported; a config using
// them gets a clear error instead of a silent misparse.

type nodeKind int

const (
	kindScalar nodeKind = iota
	kindMap
	kindSeq
)

type node struct {
	kind   nodeKind
	scalar string
	// keys preserves mapping order for deterministic error messages.
	keys   []string
	fields map[string]*node
	items  []*node
	line   int
}

func (n *node) child(key string) *node {
	if n == nil || n.kind != kindMap {
		return nil
	}
	return n.fields[key]
}

type rawLine struct {
	indent int
	text   string
	lineNo int
}

// parseYAML parses the supported subset into a node tree whose root is a
// mapping (or an empty mapping for an empty document).
func parseYAML(src string) (*node, error) {
	lines, err := scanLines(src)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return &node{kind: kindMap, fields: map[string]*node{}}, nil
	}
	pos := 0
	n, err := parseBlock(lines, &pos, lines[0].indent)
	if err != nil {
		return nil, err
	}
	if pos != len(lines) {
		return nil, fmt.Errorf("line %d: unexpected indentation", lines[pos].lineNo)
	}
	if n.kind != kindMap {
		return nil, fmt.Errorf("line %d: top level of the config must be a mapping", lines[0].lineNo)
	}
	return n, nil
}

func scanLines(src string) ([]rawLine, error) {
	var out []rawLine
	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		if strings.ContainsRune(raw, '\t') {
			trimmedPrefix := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
			if strings.ContainsRune(trimmedPrefix, '\t') {
				return nil, fmt.Errorf("line %d: tabs may not be used for indentation", lineNo)
			}
		}
		body := strings.TrimRight(raw, " \r")
		trimmed := strings.TrimLeft(body, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "---" {
			continue
		}
		out = append(out, rawLine{indent: len(body) - len(trimmed), text: trimmed, lineNo: lineNo})
	}
	return out, nil
}

// parseBlock parses consecutive lines at the given indent into a map or a
// sequence.
func parseBlock(lines []rawLine, pos *int, indent int) (*node, error) {
	if *pos >= len(lines) {
		return &node{kind: kindMap, fields: map[string]*node{}}, nil
	}
	if strings.HasPrefix(lines[*pos].text, "- ") || lines[*pos].text == "-" {
		return parseSeq(lines, pos, indent)
	}
	return parseMap(lines, pos, indent)
}

func parseMap(lines []rawLine, pos *int, indent int) (*node, error) {
	m := &node{kind: kindMap, fields: map[string]*node{}, line: lines[*pos].lineNo}
	for *pos < len(lines) {
		ln := lines[*pos]
		if ln.indent < indent {
			break
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", ln.lineNo)
		}
		if strings.HasPrefix(ln.text, "- ") || ln.text == "-" {
			break
		}
		key, rest, ok := splitKey(ln.text)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\", got %q", ln.lineNo, ln.text)
		}
		if _, dup := m.fields[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", ln.lineNo, key)
		}
		*pos++
		var val *node
		if rest != "" {
			s, err := unquote(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", ln.lineNo, err)
			}
			val = &node{kind: kindScalar, scalar: s, line: ln.lineNo}
		} else if *pos < len(lines) && (lines[*pos].indent > indent ||
			(lines[*pos].indent == indent && strings.HasPrefix(lines[*pos].text, "-"))) {
			child, err := parseBlock(lines, pos, lines[*pos].indent)
			if err != nil {
				return nil, err
			}
			val = child
		} else {
			val = &node{kind: kindScalar, scalar: "", line: ln.lineNo}
		}
		m.keys = append(m.keys, key)
		m.fields[key] = val
	}
	return m, nil
}

func parseSeq(lines []rawLine, pos *int, indent int) (*node, error) {
	s := &node{kind: kindSeq, line: lines[*pos].lineNo}
	for *pos < len(lines) {
		ln := lines[*pos]
		if ln.indent < indent || !(strings.HasPrefix(ln.text, "- ") || ln.text == "-") {
			break
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation", ln.lineNo)
		}
		item := strings.TrimSpace(strings.TrimPrefix(ln.text, "-"))
		*pos++
		if item == "" {
			if *pos >= len(lines) || lines[*pos].indent <= indent {
				return nil, fmt.Errorf("line %d: list item has no value", ln.lineNo)
			}
			child, err := parseBlock(lines, pos, lines[*pos].indent)
			if err != nil {
				return nil, err
			}
			s.items = append(s.items, child)
			continue
		}
		if key, rest, ok := splitKey(item); ok {
			// Inline first key of a mapping item: "- url: https://..."
			inner := &node{kind: kindMap, fields: map[string]*node{}, line: ln.lineNo}
			v, err := unquote(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", ln.lineNo, err)
			}
			inner.keys = append(inner.keys, key)
			inner.fields[key] = &node{kind: kindScalar, scalar: v, line: ln.lineNo}
			childIndent := ln.indent + 2
			if *pos < len(lines) && lines[*pos].indent > ln.indent {
				childIndent = lines[*pos].indent
				more, err := parseMap(lines, pos, childIndent)
				if err != nil {
					return nil, err
				}
				for _, k := range more.keys {
					if _, dup := inner.fields[k]; dup {
						return nil, fmt.Errorf("line %d: duplicate key %q", more.fields[k].line, k)
					}
					inner.keys = append(inner.keys, k)
					inner.fields[k] = more.fields[k]
				}
			}
			s.items = append(s.items, inner)
			continue
		}
		v, err := unquote(item)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", ln.lineNo, err)
		}
		s.items = append(s.items, &node{kind: kindScalar, scalar: v, line: ln.lineNo})
	}
	return s, nil
}

// splitKey splits "key: value" honouring quoted keys, and refuses to treat a
// bare "http://x" as a key/value pair.
func splitKey(text string) (key, rest string, ok bool) {
	if strings.HasPrefix(text, "\"") || strings.HasPrefix(text, "'") {
		q := text[0]
		end := strings.IndexByte(text[1:], q)
		if end < 0 {
			return "", "", false
		}
		key = text[1 : 1+end]
		remainder := strings.TrimSpace(text[2+end:])
		if !strings.HasPrefix(remainder, ":") {
			return "", "", false
		}
		return key, strings.TrimSpace(remainder[1:]), true
	}
	for i := 0; i < len(text); i++ {
		if text[i] != ':' {
			continue
		}
		if i+1 < len(text) && text[i+1] != ' ' {
			continue // e.g. "https://..." or "12:30"
		}
		k := strings.TrimSpace(text[:i])
		if k == "" || strings.ContainsAny(k, " \t") && !isSimpleKey(k) {
			return "", "", false
		}
		return k, strings.TrimSpace(text[i+1:]), true
	}
	return "", "", false
}

func isSimpleKey(k string) bool {
	for _, r := range k {
		if r == ' ' || r == '\t' {
			return false
		}
	}
	return true
}

// unquote strips surrounding quotes and trailing comments from a scalar.
func unquote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	switch s[0] {
	case '"', '\'':
		q := s[0]
		end := strings.LastIndexByte(s, q)
		if end == 0 {
			return "", fmt.Errorf("unterminated quoted value %s", s)
		}
		body := s[1:end]
		if q == '"' {
			body = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(body)
		}
		return body, nil
	}
	// Strip an unquoted trailing comment (" #" only, so URLs with '#' survive).
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s, nil
}
