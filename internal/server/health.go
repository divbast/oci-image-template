// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Health serves the health endpoint: GET or HEAD on /healthz answers 200
// with ok, and anything else 404 with nothing.
func Health() http.Handler {
	return only("/healthz", "ok")
}

// halfWithin bounds each half of [Probe]: the HEALTHCHECK's 10 s timeout
// clears both, one after the other.
const halfWithin = 4 * time.Second

// Probe checks the app is healthy, in two halves, neither of which implies
// the other: healthURL, the health port's /healthz, answers 200, and
// appURL, the app port's /, answers 200 with exactly Page. It prints
// nothing and returns 0 when both hold. Otherwise it prints one
// "<check>: <reason>" line for each half that failed, healthz or page, to
// stderr, which is the only record of why, and returns 1.
func Probe(ctx context.Context, appURL, healthURL string, stderr io.Writer) int {
	code := 0
	if err := half(ctx, healthURL, func(status int, _ []byte) error {
		if status != http.StatusOK {
			return fmt.Errorf("GET /healthz answered %d, want 200", status)
		}
		return nil
	}); err != nil {
		_, _ = fmt.Fprintln(stderr, "healthz:", err)
		code = 1
	}
	if err := half(ctx, appURL, func(status int, body []byte) error {
		if status != http.StatusOK || string(body) != Page {
			return fmt.Errorf("GET / answered %d %q, want 200 %q", status, body, Page)
		}
		return nil
	}); err != nil {
		_, _ = fmt.Fprintln(stderr, "page:", err)
		code = 1
	}
	return code
}

// half sends GET url within halfWithin, and returns what check makes of
// the answer, or why there was none.
func half(ctx context.Context, url string, check func(status int, body []byte) error) error {
	ctx, cancel := context.WithTimeout(ctx, halfWithin)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	return check(resp.StatusCode, body)
}
