<?php

// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

declare(strict_types=1);

/*
 * The endpoint half of the health definition, GET /healthz on :8081: what
 * the server needs, checked from inside the server, by the same PHP that
 * serves the page. rootfs/usr/local/bin/template-health adds the half only
 * a request to :8080 can show.
 *
 * Every check runs, and each that fails is a line of the 503, its name and
 * why. An app adds its own checks here: a database it opens, a file it
 * reads. No line names a filesystem path or repeats an exception's message.
 */

/**
 * How PHP itself reads a boolean directive: on, yes and true, or else the
 * number the value starts with. display_errors also takes stderr and stdout,
 * and both mean on.
 */
function iniEnabled(string $directive): bool
{
    $value = strtolower(trim((string) ini_get($directive)));

    return in_array($value, ['on', 'yes', 'true', 'stderr', 'stdout'], true) || (int) $value !== 0;
}

/**
 * php/template.ini, as this server applies it. Reading the live settings
 * proves the file was scanned, and that nothing scanned after it, from
 * PHP_INI_SCAN_DIR say, took any of them back: a URL stream would let PHP
 * reach out of the container, a displayed error would put a path in a
 * response, and with logging off no error would be seen at all.
 */
function checkIni(): ?string
{
    $wrong = array_keys(array_filter([
        'allow_url_fopen is on' => iniEnabled('allow_url_fopen'),
        'display_errors is on' => iniEnabled('display_errors'),
        'log_errors is off' => !iniEnabled('log_errors'),
    ]));

    return $wrong === [] ? null : implode(', ', $wrong);
}

$failures = array_filter([
    'ini' => checkIni(),
], static fn (?string $why): bool => $why !== null);

http_response_code($failures === [] ? 200 : 503);
header('Content-Type: text/plain; charset=utf-8');
header('Cache-Control: no-store');
if ($failures === []) {
    echo "ok\n";
}
foreach ($failures as $check => $why) {
    echo "$check: $why\n";
}
