// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package server_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/divbast/oci-image-template/internal/server"
)

func TestApp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		want         int
		body         string
	}{
		{method: http.MethodGet, path: "/", want: http.StatusOK, body: "Hello from oci-image-template"},
		{method: http.MethodHead, path: "/", want: http.StatusOK},
		{method: http.MethodPost, path: "/", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/index.php", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/healthz", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/nothing", want: http.StatusNotFound},
		{method: http.MethodGet, path: "//", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			server.App().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, http.NoBody))
			if rec.Code != tt.want || rec.Body.String() != tt.body {
				t.Errorf("%s %s = %d %q, want %d %q", tt.method, tt.path, rec.Code, rec.Body.String(), tt.want, tt.body)
			}
			if tt.want == http.StatusOK {
				if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("%s %s Content-Type = %q, want text/plain; charset=utf-8", tt.method, tt.path, got)
				}
			}
		})
	}
}

func TestLogged(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	h := server.Logged(server.App(), slog.New(slog.NewJSONHandler(&out, nil)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nothing", http.NoBody))

	var entry struct {
		Msg, Method, Path string
		Status            int
	}
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("the log line %q is not JSON: %v", out.String(), err)
	}
	if entry.Msg != "request" || entry.Method != http.MethodGet || entry.Path != "/nothing" || entry.Status != http.StatusNotFound {
		t.Errorf("logged %+v, want the request's method, path and status", entry)
	}
}
