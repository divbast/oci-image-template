// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Lock is a lock file: the comment that opens it, and its packages.
type Lock struct {
	Header   []string
	Packages []Locked
}

// WriteLock writes l as a pip requirements file: the header as comments,
// a --no-binary line naming every package built from source, so pip
// fetches no wheel of one, then each package as name==version with a
// --hash line for each of its files, under a comment saying why that
// version. Packages go in the order of their normalised names, and hashes
// in theirs, so the same packages always give the same bytes.
func WriteLock(w io.Writer, l Lock) error {
	pkgs := slices.Clone(l.Packages)
	slices.SortFunc(pkgs, func(a, b Locked) int { return cmp.Compare(Normalize(a.Name), Normalize(b.Name)) })
	var b strings.Builder
	for _, h := range l.Header {
		b.WriteString("# " + h + "\n")
	}
	var source []string
	for _, p := range pkgs {
		if p.FromSource {
			source = append(source, Normalize(p.Name))
		}
	}
	if len(source) > 0 {
		b.WriteString("--no-binary " + strings.Join(source, ",") + "\n")
	}
	for _, p := range pkgs {
		why := p.Why
		if p.FromSource {
			why += "; built from source"
		}
		switch {
		case p.Yanked && p.YankedReason != "":
			why += "; yanked on PyPI: " + p.YankedReason
		case p.Yanked:
			why += "; yanked on PyPI, with no reason given"
		}
		// One comment line, whatever the text: no line pip would read
		why = strings.Join(strings.Fields(why), " ")
		fmt.Fprintf(&b, "\n# %s\n%s==%s", why, Normalize(p.Name), p.Version)
		hashes := make([]string, 0, len(p.Files))
		for _, f := range p.Files {
			hashes = append(hashes, f.SHA256)
		}
		slices.Sort(hashes)
		for _, h := range hashes {
			b.WriteString(" \\\n    --hash=sha256:" + h)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteFiles writes every file of locked, one a line, as its sha256 and the
// URL PyPI serves it at, after the header as comments, in the order of
// their URLs: what a build fetches, and checks, before pip reads anything,
// so no package's code runs while the network is on.
func WriteFiles(w io.Writer, header []string, locked []Locked) error {
	var b strings.Builder
	for _, h := range header {
		b.WriteString("# " + h + "\n")
	}
	var files []File
	for _, l := range locked {
		files = append(files, l.Files...)
	}
	slices.SortFunc(files, func(a, b File) int { return cmp.Compare(a.URL, b.URL) })
	for _, f := range files {
		b.WriteString(f.SHA256 + " " + f.URL + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
