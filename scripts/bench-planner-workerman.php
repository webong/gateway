#!/usr/bin/env php
<?php

declare(strict_types=1);

/**
 * Superseded by scripts/bench-planner-hosts.php.
 *
 * The earlier version of this script started a bare Workerman process against
 * SQLite, sampled memory inside the timed loop, used one non-keep-alive client, and
 * could not run the same workload against another host. The replacement runs the
 * same authenticated planning workload, with the same client, against Workerman and
 * an Octane server in containers of the Planner image, and validates every response.
 *
 * This file only forwards to it (Workerman alone), so existing invocations keep
 * working. Pass `--hosts=workerman,frankenphp` to the new script directly for the
 * head-to-head comparison.
 */

fwrite(STDERR, "scripts/bench-planner-workerman.php now forwards to scripts/bench-planner-hosts.php --hosts=workerman.\n");

$command = escapeshellarg(PHP_BINARY).' '.escapeshellarg(__DIR__.'/bench-planner-hosts.php').' --hosts=workerman';
foreach (array_slice($argv, 1) as $argument) {
    $command .= ' '.escapeshellarg($argument);
}

passthru($command, $status);
exit($status);
