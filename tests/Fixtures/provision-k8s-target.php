<?php

declare(strict_types=1);

use Illuminate\Contracts\Console\Kernel;
use Webong\Gateway\Servers\Models\DeploymentTarget;

if ($argc !== 2 || preg_match('/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/', $argv[1]) !== 1) {
    fwrite(STDERR, "Usage: provision-k8s-target.php NAMESPACE\n");
    exit(2);
}

require '/app/vendor/autoload.php';
$app = require '/app/tests/Fixtures/octane-app/bootstrap/app.php';
$app->make(Kernel::class)->bootstrap();

$token = bin2hex(random_bytes(32));
$target = DeploymentTarget::query()->create([
    'name' => 'kubernetes-live-smoke',
    'driver' => 'kubernetes',
    'connection' => [
        'context' => 'gateway-in-cluster',
        'kubeconfig' => '/tmp/gateway-in-cluster.kubeconfig',
        'namespace' => $argv[1],
        'route_mode' => 'pod-ip',
    ],
    'worker_token_hash' => hash('sha256', $token),
]);

echo json_encode(['id' => $target->getKey(), 'worker_token' => $token], JSON_THROW_ON_ERROR)."\n";
