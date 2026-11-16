// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// page is exactly what GET / answers.
const page = "Hello from oci-image-template"

// refused are paths the app port must answer with a 404 and an empty
// body: a PHP app's script by its own name, /healthz, which only the
// health port serves, and a path nothing names.
var refused = []string{"/index.php", "/healthz", "/nope"}

// TestServes checks the app port, from a client outside the container,
// through the published port: GET / answers 200 with exactly the page, and
// every other path a 404 with an empty body, because 8080 is an
// allowlist.
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
		harness.Equal(checks, what+" has an empty body", "", string(resp.Body))
	}
}
