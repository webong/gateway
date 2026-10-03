<?php

declare(strict_types=1);

use Illuminate\Contracts\Console\Kernel;
use Webong\Gateway\Servers\Models\Server;

if ($argc !== 2 || preg_match('/^[a-f0-9-]{36}$/', $argv[1]) !== 1) {
    fwrite(STDERR, "Usage: provision-k8s-server.php TARGET_ID\n");
    exit(2);
}

require '/app/vendor/autoload.php';
$app = require '/app/tests/Fixtures/octane-app/bootstrap/app.php';
$app->make(Kernel::class)->bootstrap();

$server = Server::query()->create([
    'type' => 'centrifugo',
    'name' => 'kubernetes-live-workload',
    'hostname' => 'live.example.test',
    'path' => '/connection',
    'driver' => 'kubernetes',
    'target_id' => $argv[1],
    'desired_state' => 'running',
    'replicas' => 1,
    'revision' => 1,
    'configuration' => [
        'client_token_hmac_secret_key' => 'disposable-client-secret',
        'client_allowed_origins' => [],
        'client_insecure' => false,
        'channel_without_namespace_allow_subscribe_for_client' => false,
        'http_api_key' => 'disposable-api-key',
        'engine_type' => 'memory',
        'prometheus_enabled' => false,
        'log_level' => 'info',
    ],
]);

echo $server->getKey()."\n";
