<?php

// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

declare(strict_types=1);

/*
 * The app: the one page :8080 serves, at /. PHP_SAPI reads frankenphp only
 * when FrankenPHP's server runs the script, so the exact answer shows Caddy
 * handing / to PHP, and PHP running this file. The health probe and the
 * acceptance suite both check it, word for word.
 */

header('Content-Type: text/plain; charset=utf-8');
echo 'Hello from ' . PHP_SAPI . "\n";
