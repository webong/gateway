<?php

declare(strict_types=1);

if ($argc !== 3 || ! in_array($argv[1], ['instance', 'session'], true)) {
    fwrite(STDERR, "Usage: provision-k8s-observe.php instance SERVER_ID | session NODE_ID\n");
    exit(2);
}

$database = new PDO('sqlite:/tmp/registry.sqlite');
$database->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
if ($argv[1] === 'instance') {
    $query = $database->prepare('SELECT id, state, host, port, heartbeat_at FROM instances WHERE server_id = ? ORDER BY created_at DESC LIMIT 1');
} else {
    $query = $database->prepare('SELECT session_id, heartbeat_at FROM nodes WHERE id = ?');
}
$query->execute([$argv[2]]);
echo json_encode($query->fetch(PDO::FETCH_ASSOC) ?: new stdClass(), JSON_THROW_ON_ERROR)."\n";
