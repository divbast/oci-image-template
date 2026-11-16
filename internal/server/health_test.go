// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package server_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/divbast/oci-image-template/internal/server"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		want         int
		body         string
	}{
		{method: http.MethodGet, path: "/healthz", want: http.StatusOK, body: "ok"},
		{method: http.MethodGet, path: "/", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/index.php", want: http.StatusNotFound},
		{method: http.MethodPost, path: "/healthz", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			server.Health().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody))
			if rec.Code != tt.want || rec.Body.String() != tt.body {
				t.Errorf("%s %s = %d %q, want %d %q", tt.method, tt.path, rec.Code, rec.Body.String(), tt.want, tt.body)
			}
		})
	}
}

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

	app := httptest.NewServer(server.App())
	t.Cleanup(app.Close)
	health := httptest.NewServer(server.Health())
	t.Cleanup(health.Close)

	tests := []struct {
		name            string
		appURL, health  string
		wantCode        int
		wantStderrLines []string
	}{
		{name: "healthy", appURL: app.URL + "/", health: health.URL + "/healthz"},
		{
			name: "page-wrong", appURL: answering(t, http.StatusOK, "Hello from somewhere else"), health: health.URL + "/healthz",
			wantCode: 1, wantStderrLines: []string{`page: GET / answered 200 "Hello from somewhere else", want 200 "Hello from oci-image-template"`},
		},
		{
			name: "healthz-503", appURL: app.URL + "/", health: answering(t, http.StatusServiceUnavailable, "down"),
			wantCode: 1, wantStderrLines: []string{"healthz: GET /healthz answered 503, want 200"},
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
			code := server.Probe(t.Context(), tt.appURL, tt.health, &stderr)
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
