<?php

declare(strict_types=1);

use Webong\Gateway\Companion\CompanionActionExecutionStore;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;

if ($argc < 3 || ! in_array($argv[1], ['capability', 'invoke'], true)) {
    fwrite(STDERR, "Usage: companion-image-request.php {capability MODE|absent|invoke EXECUTION RESPONSE}\n");
    exit(2);
}

$app = require '/gateway/app/bootstrap.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
$registry = $app->make(CompanionActionRegistry::class);

if ($argv[1] === 'capability') {
    $capability = $registry->capability('gateway.hookscript.execute');
    if ($argv[2] === 'absent') {
        if ($capability !== null) {
            fwrite(STDERR, "HookScript unexpectedly advertised a provider.\n");
            exit(1);
        }

        exit(0);
    }
    if ($capability === null || $capability->mode !== $argv[2]) {
        fwrite(STDERR, "HookScript did not advertise exactly one {$argv[2]} provider.\n");
        exit(1);
    }

    echo json_encode($capability->toArray(), JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES)."\n";
    exit(0);
}

$response = $argv[3] ?? '';
if (preg_match('/\A[a-z0-9-]{1,64}\z/D', $response) !== 1) {
    fwrite(STDERR, "Invalid smoke response fixture.\n");
    exit(2);
}
$execution = $argv[2];
$result = $app->make(CompanionActionExecutionStore::class)->execute(new CompanionActionInvocation(
    action: 'gateway.hookscript.execute',
    executionId: $execution,
    idempotencyKey: $execution,
    input: [
        'source' => "respond('{$response}');",
        'event' => [],
        'variables' => [],
    ],
    deadline: new DateTimeImmutable('+30 seconds'),
    companionVersion: '1.0.0',
));

echo json_encode($result->toArray(), JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES)."\n";
