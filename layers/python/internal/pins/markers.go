// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"errors"
	"fmt"
	"strings"
)

// MarkerEnv is the platform a requirement's environment marker is judged
// for: PEP 508's python_version, sys_platform and platform_system, the
// three requirements files name for Linux, Windows and macOS.
type MarkerEnv struct{ PythonVersion, SysPlatform, PlatformSystem string }

// EvalMarker judges expr, a PEP 508 environment marker, for env. It reads
// comparisons joined by "and" and "or", in parentheses or not, each a
// variable of env against a quoted value, either way round. A variable it
// does not know, or an operator it does not read, is an error rather than
// a guess: a wrong guess would put a package in the image, or leave one
// out, with no word.
func EvalMarker(expr string, env MarkerEnv) (bool, error) {
	toks, err := tokenize(expr)
	if err != nil {
		return false, fmt.Errorf("marker %q: %w", expr, err)
	}
	p := &markerParser{toks: toks, env: env}
	v, err := p.or()
	if err == nil && p.pos < len(p.toks) {
		err = fmt.Errorf("unexpected %s", p.toks[p.pos].text)
	}
	if err != nil {
		return false, fmt.Errorf("marker %q: %w", expr, err)
	}
	return v, nil
}

// token is one word of a marker: an identifier, a quoted value, an
// operator or a parenthesis.
type token struct {
	text   string
	quoted bool
}

func tokenize(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '(' || c == ')':
			toks = append(toks, token{text: string(c)})
			i++
		case c == '\'' || c == '"':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				return nil, errors.New("a value has no closing quote")
			}
			toks = append(toks, token{text: s[i+1 : i+1+end], quoted: true})
			i += end + 2
		case strings.ContainsRune("=!<>~", rune(c)):
			j := i
			for j < len(s) && strings.ContainsRune("=!<>~", rune(s[j])) {
				j++
			}
			toks = append(toks, token{text: s[i:j]})
			i = j
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t()'\"=!<>~", rune(s[j])) {
				j++
			}
			toks = append(toks, token{text: s[i:j]})
			i = j
		}
	}
	return toks, nil
}

type markerParser struct {
	toks []token
	pos  int
	env  MarkerEnv
}

func (p *markerParser) peek(text string) bool {
	return p.pos < len(p.toks) && !p.toks[p.pos].quoted && p.toks[p.pos].text == text
}

func (p *markerParser) or() (bool, error) {
	v, err := p.and()
	for err == nil && p.peek("or") {
		p.pos++
		var w bool
		w, err = p.and()
		v = v || w
	}
	return v, err
}

func (p *markerParser) and() (bool, error) {
	v, err := p.atom()
	for err == nil && p.peek("and") {
		p.pos++
		var w bool
		w, err = p.atom()
		v = v && w
	}
	return v, err
}

func (p *markerParser) atom() (bool, error) {
	if p.peek("(") {
		p.pos++
		v, err := p.or()
		if err != nil {
			return false, err
		}
		if !p.peek(")") {
			return false, errors.New("a parenthesis is not closed")
		}
		p.pos++
		return v, nil
	}
	if p.pos+3 > len(p.toks) {
		return false, errors.New("it ends where a comparison should follow")
	}
	left, op, right := p.toks[p.pos], p.toks[p.pos+1], p.toks[p.pos+2]
	p.pos += 3
	if op.quoted || left.quoted == right.quoted {
		return false, fmt.Errorf("%s %s %s is no comparison of a variable with a quoted value", left.text, op.text, right.text)
	}
	variable, value, reversed := left.text, right.text, false
	if left.quoted {
		variable, value, reversed = right.text, left.text, true
	}
	return p.compare(variable, op.text, value, reversed)
}

// compare judges variable op value, or value op variable when reversed.
func (p *markerParser) compare(variable, op, value string, reversed bool) (bool, error) {
	if !map[string]bool{"==": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true}[op] {
		return false, fmt.Errorf("the operator %s is not one this tool reads", op)
	}
	var have string
	switch variable {
	case "python_version":
		order, err := compareVersions(p.env.PythonVersion, value)
		if err != nil {
			return false, err
		}
		if reversed {
			order = -order
		}
		return map[string]bool{"==": order == 0, "!=": order != 0, ">=": order >= 0, "<=": order <= 0, ">": order > 0, "<": order < 0}[op], nil
	case "sys_platform":
		have = p.env.SysPlatform
	case "platform_system":
		have = p.env.PlatformSystem
	default:
		return false, fmt.Errorf("the variable %s is not one this tool knows the image's value of", variable)
	}
	switch op {
	case "==":
		return have == value, nil
	case "!=":
		return have != value, nil
	}
	return false, fmt.Errorf("%s compares as text, so only == and != apply, not %s", variable, op)
}
