<?php

declare(strict_types=1);

if (($argc !== 3 && $argc !== 4) || filter_var($argv[1], FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false ||
    filter_var($argv[2], FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false) {
    fwrite(STDERR, "Usage: seed.php HTTP_BACKEND_IPV4 WEBSOCKET_BACKEND_IPV4 [SQLITE_PATH]\n");
    exit(2);
}

$database = new PDO('sqlite:'.($argv[3] ?? '/data/gateway.sqlite'));
$database->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
$now = gmdate('Y-m-d H:i:s');
$database->beginTransaction();
$database->prepare('INSERT INTO servers (id, type, name, hostname, path, driver, desired_state, replicas, revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)')
    ->execute(['11111111-1111-4111-8111-111111111111', 'centrifugo', 'container-managed-smoke', 'managed.example.test', '/managed', 'kubernetes', 'running', 1, 1, $now, $now]);
$database->prepare('INSERT INTO nodes (id, drivers, heartbeat_at, created_at, updated_at, session_id) VALUES (?, ?, ?, ?, ?, ?)')
    ->execute(['container-managed-node', '["kubernetes"]', $now, $now, $now, 'container-smoke']);
$database->prepare('INSERT INTO instances (id, server_id, node_id, runtime, host, port, state, revision, heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)')
    ->execute(['22222222-2222-4222-8222-222222222222', '11111111-1111-4111-8111-111111111111', 'container-managed-node', 'kubernetes', $argv[1], 9090, 'running', 1, $now, $now, $now]);
$database->prepare('INSERT INTO servers (id, type, name, hostname, path, driver, desired_state, replicas, revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)')
    ->execute(['33333333-3333-4333-8333-333333333333', 'reverb', 'container-websocket-smoke', 'managed.example.test', '/socket', 'kubernetes', 'running', 1, 1, $now, $now]);
$database->prepare('INSERT INTO instances (id, server_id, node_id, runtime, host, port, state, revision, heartbeat_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)')
    ->execute(['44444444-4444-4444-8444-444444444444', '33333333-3333-4333-8333-333333333333', 'container-managed-node', 'kubernetes', $argv[2], 9091, 'running', 1, $now, $now, $now]);
$database->commit();
