// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package probe is the Python layer's health probe, which the image's
// HEALTHCHECK runs as divbast-template health. An upstream Python app cannot
// host a probe subcommand, so the probe is a small binary of its own. Both
// halves are on the app port: the app serves /healthz beside its page.
//
// Unlike the Go core's probe, which it is copied from, it follows no
// redirect, and reports one as the status it is. A Python app's framework
// commonly redirects, to a URL base, to HTTPS or to a login page, and a
// half that followed one would pass on whatever answered where it led,
// another half's answer among them.
package probe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Page is exactly what GET / answers.
const Page = "Hello from oci-image-template"

// halfWithin bounds each half of [Probe]: the HEALTHCHECK's 10 s timeout
// clears both, one after the other.
const halfWithin = 4 * time.Second

// Probe checks the app is healthy, in two halves, neither of which implies
// the other: healthURL, the app's /healthz, answers 200, and appURL, its
// /, answers 200 with exactly Page. It prints nothing and returns 0 when
// both hold. Otherwise it prints one "<check>: <reason>" line for each
// half that failed, healthz or page, to stderr, which is the only record
// of why, and returns 1.
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
// the answer, a redirect's included, or why there was none.
func half(ctx context.Context, url string, check func(status int, body []byte) error) error {
	ctx, cancel := context.WithTimeout(ctx, halfWithin)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := noRedirects.Do(req)
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

// noRedirects reports a redirect as its status instead of following it,
// so each half judges the answer of the URL it was given.
var noRedirects = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
