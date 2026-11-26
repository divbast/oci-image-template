// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Args is what the tool reads from the Containerfile: the Python the image
// runs, as major.minor.
type Args struct {
	PythonVersion string
}

// pythonTag is a DHI Python image's version, in its tag.
var pythonTag = regexp.MustCompile(`dhi\.io/python:(\d+)\.(\d+)\.(\d+)-`)

// arg returns the value of the one ARG name=value line of containerfile.
func arg(containerfile []byte, name string) (string, error) {
	m := regexp.MustCompile(`(?m)^ARG `+regexp.QuoteMeta(name)+`=(.*)$`).FindAllSubmatch(containerfile, -1)
	if len(m) != 1 {
		return "", fmt.Errorf("the Containerfile must hold one ARG %s=<value> line, not %d", name, len(m))
	}
	return strings.TrimSpace(string(m[0][1])), nil
}

// ReadArgs reads Args from containerfile. Every dhi.io/python image it
// names must be one Python, since the venv the -dev image builds runs on
// the runtime image's.
func ReadArgs(containerfile []byte) (Args, error) {
	var versions []string
	for _, m := range pythonTag.FindAllSubmatch(containerfile, -1) {
		if v := fmt.Sprintf("%s.%s.%s", m[1], m[2], m[3]); !slices.Contains(versions, v) {
			versions = append(versions, v)
		}
	}
	switch len(versions) {
	case 0:
		return Args{}, errors.New("the Containerfile names no dhi.io/python:<x.y.z>- image, so nothing says which Python the venv is for")
	case 1:
		return Args{PythonVersion: versions[0][:strings.LastIndex(versions[0], ".")]}, nil
	default:
		return Args{}, fmt.Errorf("the Containerfile names Pythons %s: the -dev image and the runtime must be one", strings.Join(versions, " and "))
	}
}

// argRef is ${NAME}, an ARG named in a requirements URL.
var argRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand returns s with each ${NAME} in it the value of containerfile's one
// ARG NAME=<value> line, as a build stage reads it, so a URL can name the
// commit the Containerfile pins.
func Expand(containerfile []byte, s string) (string, error) {
	var errs []error
	out := argRef.ReplaceAllStringFunc(s, func(ref string) string {
		value, err := arg(containerfile, argRef.FindStringSubmatch(ref)[1])
		errs = append(errs, err)
		return value
	})
	if err := errors.Join(errs...); err != nil {
		return "", err
	}
	if strings.Contains(out, "${") {
		return "", fmt.Errorf("%s holds a ${ with no ARG name and } after it", s)
	}
	return out, nil
}
