// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Command template is the template's app, and the image's PID 1.
//
// Usage:
//
//	template serve    serve the page on :8080 and /healthz on :8081
//	template health   check both, as the HEALTHCHECK does
//	template version  print the version
//
// serve logs each request as JSON to stdout, and on SIGTERM or SIGINT
// stops taking requests, finishes those it has within 5 s, and exits 0:
// the kernel drops a signal PID 1 does not handle, so without this a stop
// would wait out the engine's timeout and kill it.
//
// health prints nothing when both halves hold, and one "<check>: <reason>"
// line for each that does not.
//
// Exit codes:
//
//	0  served until stopped, healthy, or the version printed
//	1  a listener failed, or a half of health did not hold
//	2  the command line is wrong
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/divbast/oci-image-template/internal/server"
)

// version is set at build time, with -ldflags "-X main.version=…".
var version = "dev"

const (
	appAddr    = ":8080"
	healthAddr = ":8081"
	// stopWithin bounds the shutdown a stop asks for.
	stopWithin = 5 * time.Second
)

func main() {
	if len(os.Args) != 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "template:", err)
			os.Exit(1)
		}
	case "health":
		os.Exit(server.Probe(context.Background(), "http://127.0.0.1:8080/", "http://127.0.0.1:8081/healthz", os.Stderr))
	case "version":
		fmt.Println(version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: template serve|health|version")
	os.Exit(2)
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	servers := []*http.Server{
		{Addr: appAddr, Handler: server.Logged(server.App(), log), ReadHeaderTimeout: 5 * time.Second},
		{Addr: healthAddr, Handler: server.Health(), ReadHeaderTimeout: 5 * time.Second},
	}
	failed := make(chan error, len(servers))
	for _, s := range servers {
		go func() {
			if err := s.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				failed <- fmt.Errorf("listening on %s: %w", s.Addr, err)
			}
		}()
	}
	log.Info("serving", slog.String("app", appAddr), slog.String("health", healthAddr), slog.String("version", version))

	var err error
	select {
	case err = <-failed:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), stopWithin)
	defer cancel()
	for _, s := range servers {
		err = errors.Join(err, s.Shutdown(shutdown))
	}
	return err
}
