// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"os/exec"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// stopWithin is how long a stop may take: the app's own shutdown bound,
// with the engine's room besides, and well inside the 10 s after which an
// engine kills a PID 1 that never stopped.
const stopWithin = 5 * time.Second

// TestStopsPromptly stops a serving container as a user does, with
// `<engine> stop`, and checks it exits 0 well before the engine's
// timeout. The kernel drops a signal to PID 1 that it does not handle, so
// a server that never handles SIGTERM keeps running until the engine
// kills it, ten seconds on, with exit 137.
func TestStopsPromptly(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()

	s, err := e.Serve(ctx, t, &harness.ServeSpec{
		Run:     harness.RunSpec{Image: image, Confine: harness.Contract(tmpfsSize)},
		AppPort: appPort,
	})
	checks := harness.NewChecks(t)
	if err != nil {
		checks.True("the container serves", false, err.Error())
		return
	}

	start := time.Now()
	out, err := exec.CommandContext(ctx, e.Name(), "stop", s.C.ID).CombinedOutput() //nolint:gosec // G204: the engine and the container are the harness's own
	took := time.Since(start)
	checks.True(e.Name()+" stop returns", err == nil, string(out))
	checks.True("the container stops within "+stopWithin.String(), took < stopWithin, took.String())

	code, err := s.C.WaitExit(ctx, time.Second)
	if err != nil {
		checks.True("the container exits", false, err.Error())
		return
	}
	harness.Equal(checks, "the container exits 0", 0, code)
}
