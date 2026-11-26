// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// context is how many unchanged lines a hunk shows around a change.
const contextLines = 3

// Diff is a unified diff from old to updated, the file at path, with
// three lines of context: "" when they are the same. The files are a few
// hundred lines at most, so it compares every line with every other.
func Diff(path string, old, updated []byte) string {
	if bytes.Equal(old, updated) {
		return ""
	}
	a, b := lines(string(old)), lines(string(updated))
	// lcs[i][j] is the length of the longest common run of a[i:] and b[j:]
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte // ' ', '-' or '+'
		text string
		i, j int // the line's index in a and b, before it
	}
	var ops []op
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, op{' ', a[i], i, j})
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, op{'+', b[j], i, j})
			j++
		default:
			ops = append(ops, op{'-', a[i], i, j})
			i++
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", path, path)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(0, k-contextLines)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*contextLines || run == len(ops) {
				end = min(len(ops), end+contextLines)
				break
			}
			end = run
		}
		oldN, newN := 0, 0
		for _, o := range ops[start:end] {
			if o.kind != '+' {
				oldN++
			}
			if o.kind != '-' {
				newN++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", ops[start].i+1, oldN, ops[start].j+1, newN)
		for _, o := range ops[start:end] {
			out.WriteString(string(o.kind) + o.text + "\n")
		}
		k = end
	}
	return out.String()
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
