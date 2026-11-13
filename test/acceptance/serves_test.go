// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/divbast/ci/harness"
)

// page is exactly what GET / answers. PHP_SAPI in it is frankenphp only
// when FrankenPHP's server ran the script: Caddy serving the file as it
// stands would answer its source, and php-cli would say cli.
const page = "Hello from frankenphp\n"

// refused are paths the app port must answer with a 404 that shows none
// of the script: the script by its own name, which only / may run, and a
// path nothing names. A catch-all that fell through to a file server
// would answer the first with the script's source.
var refused = []string{"/index.php", "/nope"}

// phpOpenTag starts the script's source, which no answer may carry.
const phpOpenTag = "<?php"

// TestServes checks the app port, from a client outside the container,
// through the published port: GET / answers 200 with exactly the page, and
// every other path a 404 that shows none of the script, because the
// Caddyfile's :8080 is an allowlist.
func TestServes(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()

	s, err := e.Serve(ctx, t, &harness.ServeSpec{
		Run: harness.RunSpec{
			Image:   image,
			Confine: harness.Contract(tmpfsSize),
		},
		AppPort: appPort,
	})
	checks := harness.NewChecks(t)
	if err != nil {
		checks.True("GET / from the host gets an answer", false, err.Error())
		return
	}

	resp, err := s.Get(ctx, "/", nil)
	harness.Answers(t, "GET / from the host answers 200", http.StatusOK, resp, err)
	if err == nil {
		harness.Equal(checks, "GET / from the host is exactly the page", page, string(resp.Body))
	}

	for _, path := range refused {
		what := "GET " + path + " from the host"
		resp, err = s.Get(ctx, path, nil)
		harness.Answers(t, what+" answers 404", http.StatusNotFound, resp, err)
		if err != nil {
			continue
		}
		// Not harness.Lacks: the allowlist's catch-all is Caddy's respond
		// 404, whose body is empty, and Lacks fails on a body with nothing
		// in it to look through.
		body := string(resp.Body)
		checks.True(what+" shows none of the script", !strings.Contains(body, phpOpenTag),
			fmt.Sprintf("%d bytes: %s", len(body), harness.Clip(body)))
	}
}
