// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package pins

import (
	"strings"
	"testing"
)

// containerfile holds the lines the tool reads, as an app's Containerfile
// writes them: the commit of the upstream it builds, and DHI's Python.
const containerfile = `# The app
ARG APP_VERSION=1.2.0
ARG APP_COMMIT=ffc619947b5ba4e6520e3a0bc70680024e184b2f
ARG RUNTIME_IMAGE=dhi.io/python:3.15.2-debian13@sha256:d5387cad673412ef60e7fdadec757bd653c7dc289bfdccb4beff6d1ccec94f36

FROM dhi.io/python:3.15.2-debian13-dev@sha256:a1069444b00f18a15f0088a7f6c33ed6070d7f2593919130892b22c5a4f124b7 AS deps
ARG APP_COMMIT
FROM ${RUNTIME_IMAGE}
`

func TestReadArgs(t *testing.T) {
	t.Parallel()
	if got, err := ReadArgs([]byte(containerfile)); err != nil || got != (Args{PythonVersion: "3.15"}) {
		t.Errorf("ReadArgs() = %+v, %v; want Python 3.15", got, err)
	}
	for _, tt := range []struct{ name, text, want string }{
		// The -dev image and the runtime must be one Python
		{"two-pythons", strings.Replace(containerfile, "3.15.2-debian13-dev", "3.14.10-debian13-dev", 1), "3.14.10"},
		{"no-python", strings.ReplaceAll(containerfile, "dhi.io/python:", "dhi.io/golang:"), "dhi.io/python"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ReadArgs([]byte(tt.text)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ReadArgs() = %v, want an error naming %q", err, tt.want)
			}
		})
	}
}

// TestExpand is a requirements URL naming the Containerfile's ARGs, as
// ${NAME}: each is the value of the one ARG NAME=<value> line, and an ARG
// with no such line, or two, is an error naming it.
func TestExpand(t *testing.T) {
	t.Parallel()
	const url = "https://raw.githubusercontent.com/owner/app/${APP_COMMIT}/requirements.txt"
	got, err := Expand([]byte(containerfile), url)
	if want := "https://raw.githubusercontent.com/owner/app/ffc619947b5ba4e6520e3a0bc70680024e184b2f/requirements.txt"; err != nil || got != want {
		t.Errorf("Expand() = %q, %v; want %q", got, err, want)
	}
	if got, err = Expand([]byte(containerfile), "https://example.com/${APP_VERSION}/${APP_COMMIT}"); err != nil ||
		got != "https://example.com/1.2.0/ffc619947b5ba4e6520e3a0bc70680024e184b2f" {
		t.Errorf("Expand() of two ARGs = %q, %v", got, err)
	}
	for _, tt := range []struct{ name, text, url, want string }{
		{"undefined", containerfile, "https://example.com/${APP_REF}", "APP_REF"},
		{"twice", containerfile + "ARG APP_COMMIT=0000000000000000000000000000000000000000\n", url, "APP_COMMIT"},
		{"unclosed", containerfile, "https://example.com/${APP_COMMIT", "${APP_COMMIT"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got, err := Expand([]byte(tt.text), tt.url); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Expand(%q) = %q, %v; want an error naming %q", tt.url, got, err, tt.want)
			}
		})
	}
}
