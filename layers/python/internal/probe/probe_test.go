// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package probe_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/divbast/oci-image-template/layers/python/internal/probe"
)

// answering is a server that answers every request with status and body.
func answering(t *testing.T, status int, body string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

// redirecting is a server that answers every request with a 302 to
// location.
func redirecting(t *testing.T, location string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

// closedURL is a URL nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr + "/"
}

func TestProbe(t *testing.T) {
	t.Parallel()

	app := answering(t, http.StatusOK, probe.Page)
	health := answering(t, http.StatusOK, "ok")

	tests := []struct {
		name            string
		appURL, health  string
		wantCode        int
		wantStderrLines []string
	}{
		{name: "healthy", appURL: app + "/", health: health + "/healthz"},
		{
			name: "page-wrong", appURL: answering(t, http.StatusOK, "Hello from somewhere else"), health: health + "/healthz",
			wantCode: 1, wantStderrLines: []string{`page: GET / answered 200 "Hello from somewhere else", want 200 "Hello from oci-image-template"`},
		},
		{
			name: "healthz-503", appURL: app + "/", health: answering(t, http.StatusServiceUnavailable, "down"),
			wantCode: 1, wantStderrLines: []string{"healthz: GET /healthz answered 503, want 200"},
		},
		{
			// Followed, the redirect would pass /healthz on the page's
			// answer
			name: "healthz-redirects", appURL: app + "/", health: redirecting(t, app+"/"),
			wantCode: 1, wantStderrLines: []string{"healthz: GET /healthz answered 302, want 200"},
		},
		{
			// Followed, the redirect would pass the page on whatever
			// answers where it leads
			name: "page-redirects", appURL: redirecting(t, app+"/"), health: health + "/healthz",
			wantCode: 1, wantStderrLines: []string{`page: GET / answered 302 "", want 200 "Hello from oci-image-template"`},
		},
		{
			name: "both-unreachable", appURL: closedURL(t), health: closedURL(t),
			wantCode: 1, wantStderrLines: []string{"healthz: ", "page: "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			code := probe.Probe(t.Context(), tt.appURL, tt.health, &stderr)
			if code != tt.wantCode {
				t.Errorf("Probe() = %d, want %d; stderr %q", code, tt.wantCode, stderr.String())
			}
			lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			if tt.wantCode == 0 {
				if stderr.Len() != 0 {
					t.Errorf("Probe() printed %q, want nothing when healthy", stderr.String())
				}
				return
			}
			if len(lines) != len(tt.wantStderrLines) {
				t.Fatalf("Probe() printed %q, want one line for each half that failed: %q", lines, tt.wantStderrLines)
			}
			for i, want := range tt.wantStderrLines {
				if !strings.HasPrefix(lines[i], want) {
					t.Errorf("line %d = %q, want it to start %q", i+1, lines[i], want)
				}
			}
		})
	}
}
