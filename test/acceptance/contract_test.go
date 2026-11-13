// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"slices"
	"testing"
	"time"

	"github.com/divbast/ci/contract"
	"github.com/divbast/ci/harness"
)

// What the image's contract says of every container here.
const (
	// tmpfsSize is the size of /tmp the README states. Every container
	// gets exactly this, so no check passes on more room than the
	// contract grants.
	tmpfsSize = "16m"
	// appPort serves the page, and healthPort only /healthz.
	appPort    = 8080
	healthPort = 8081
)

// serverArgv is PID 1's argv: the image's ENTRYPOINT, since nothing runs
// before the server.
var serverArgv = []string{harness.FrankenPHP, "run", "--config", "/etc/frankenphp/Caddyfile"}

// healthCmd is the shipped health probe, as the image's HEALTHCHECK runs
// it and a Podman user passes it with --health-cmd.
var healthCmd = []string{harness.FrankenPHP, "php-cli", "/usr/local/bin/template-health"}

// TestContract runs divbast's confinement contract against the image, on a
// network with no route out: no capability, a read-only root, uid 65532,
// the server as PID 1, no shell, two listeners, no egress, the HEALTHCHECK
// and the health port.
func TestContract(t *testing.T) {
	t.Parallel()
	_, image := underTest(t)
	contract.Run(t, &contract.Config{
		Image:      image,
		AppPort:    appPort,
		HealthPort: healthPort,
		TmpfsSize:  tmpfsSize,
		PID1:       serverArgv,
		HealthCmd:  healthCmd,
		Healthcheck: contract.Healthcheck{
			Test:        slices.Concat([]string{"CMD"}, healthCmd),
			Interval:    30 * time.Second,
			Timeout:     10 * time.Second,
			StartPeriod: 60 * time.Second,
			Retries:     3,
		},
		// The health port's root, and the page's script by its own name:
		// neither may answer there
		HealthOnly404: []string{"/", "/index.php"},
	})
}
