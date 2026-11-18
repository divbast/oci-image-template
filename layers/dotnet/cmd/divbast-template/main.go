// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Command divbast-template is the .NET layer's health probe: the image's
// HEALTHCHECK runs it, because an upstream .NET app cannot host a probe
// subcommand of its own.
//
// Usage:
//
//	divbast-template health   check the page and /healthz, both on :8080
//	divbast-template version  print the version
//
// health prints nothing when both halves hold, and one "<check>: <reason>"
// line for each that does not.
//
// Exit codes:
//
//	0  healthy, or the version printed
//	1  a half of health did not hold
//	2  the command line is wrong
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/divbast/oci-image-template/layers/dotnet/internal/probe"
)

// version is set at build time, with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) != 2 {
		usage()
	}
	switch os.Args[1] {
	case "health":
		os.Exit(probe.Probe(context.Background(), "http://127.0.0.1:8080/", "http://127.0.0.1:8080/healthz", os.Stderr))
	case "version":
		fmt.Println(version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: divbast-template health|version")
	os.Exit(2)
}
