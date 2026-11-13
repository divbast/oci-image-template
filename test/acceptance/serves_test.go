// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// page is exactly what GET / answers. PHP_SAPI in it is frankenphp only
// when FrankenPHP's server ran the script: Caddy serving the file as it
// stands would answer its source, and php-cli would say cli.
const page = "Hello from frankenphp\n"

// portServes is the host port TestServes publishes the app port on. The
// harness cannot read back a port the engine picked, so it is fixed.
const portServes = 18480

// serveWithin bounds how long a container may take to serve, and
// attemptWithin each request while it waits.
const (
	serveWithin   = time.Minute
	attemptWithin = 5 * time.Second
)

// TestServes checks the app serves its page to a client outside the
// container: GET / through the published app port answers 200 with
// exactly the page.
func TestServes(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()

	network, err := e.Network(ctx, t, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.Start(ctx, t, &harness.RunSpec{
		Image:   image,
		Network: network,
		Confine: harness.Contract(tmpfsSize),
		Publish: []string{fmt.Sprintf("127.0.0.1:%d:%d", portServes, appPort)},
	})
	if err != nil {
		t.Fatal(err)
	}

	status, body, err := getWhenServing(ctx, c, fmt.Sprintf("http://127.0.0.1:%d/", portServes))
	checks := harness.NewChecks(t)
	if err != nil {
		checks.True("GET / from the host gets an answer", false, err.Error())
		return
	}
	harness.Equal(checks, "GET / from the host answers 200", http.StatusOK, status)
	harness.Equal(checks, "GET / from the host is exactly the page", page, body)
}

// getWhenServing sends GET url from the host once a second until any
// status comes back, and returns it and the body. A container that stops
// first, or one still not serving after serveWithin, is an error carrying
// its log, so a server that died says why.
func getWhenServing(ctx context.Context, c *harness.Container, url string) (status int, body string, err error) {
	deadline := time.Now().Add(serveWithin)
	for {
		status, body, err = get(ctx, url)
		if err == nil {
			return status, body, nil
		}
		ins, ierr := c.Inspect(ctx)
		switch {
		case ierr != nil:
			return 0, "", fmt.Errorf("%w; and then: %w", err, ierr)
		case !ins.State.Running:
			return 0, "", withLog(ctx, c, fmt.Errorf("container %s stopped, exit status %d, before GET %s answered", c, ins.State.ExitCode, url))
		case !time.Now().Before(deadline):
			return 0, "", withLog(ctx, c, fmt.Errorf("GET %s got no answer within %s: %w", url, serveWithin, err))
		}
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// get sends one GET url from the host, bounded by attemptWithin, and reads
// the whole answer.
func get(ctx context.Context, url string) (status int, body string, err error) {
	ctx, cancel := context.WithTimeout(ctx, attemptWithin)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, "", err
	}
	resp, err := harness.HostClient().Do(req)
	if err != nil {
		return 0, "", err
	}
	b, err := io.ReadAll(resp.Body)
	if err = errors.Join(err, resp.Body.Close()); err != nil {
		return 0, "", fmt.Errorf("GET %s: reading the answer: %w", url, err)
	}
	return resp.StatusCode, string(b), nil
}

// withLog adds c's log to err.
func withLog(ctx context.Context, c *harness.Container, err error) error {
	logs, lerr := c.Logs(ctx)
	if lerr != nil {
		return fmt.Errorf("%w; its log could not be read: %w", err, lerr)
	}
	return fmt.Errorf("%w; its log:\n%s", err, strings.TrimSpace(logs))
}
