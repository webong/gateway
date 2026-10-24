<?php

declare(strict_types=1);

use Webong\Gateway\Companion\ExtProcessCompanionActionProvider;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;

it('rejects dynamic companion output above the deployment ceiling', function (): void {
    $directory = sys_get_temp_dir().'/gateway-companion-output-'.bin2hex(random_bytes(6));
    mkdir($directory, 0700, true);
    $script = $directory.'/companion.php';
    file_put_contents($script, "<?php fwrite(STDOUT, str_repeat('x', 1025));");
    $descriptor = [
        'apiVersion' => 'ext.plugin/v1',
        'identity' => ['id' => 'example/noisy', 'revision' => 'test-r1', 'version' => '1.0.0'],
        'contracts' => [[
            'name' => 'gateway.companion',
            'version' => 'v1',
            'operations' => [['name' => 'test.noisy']],
        ]],
    ];
    $provider = new ExtProcessCompanionActionProvider(
        extensionId: 'example/noisy',
        revision: 'test-r1',
        providedCapability: new CompanionActionCapability('test.noisy', '1.0.0'),
        descriptor: $descriptor,
        executable: $script,
        workingDirectory: $directory,
        maxRuntimeSeconds: 10,
        maxOutputBytes: 1024,
    );
    $invocation = new CompanionActionInvocation(
        action: 'test.noisy',
        executionId: 'noisy-1',
        idempotencyKey: 'noisy-1',
        input: [],
        deadline: new DateTimeImmutable('+30 seconds'),
        companionVersion: '1.0.0',
    );

    try {
        expect(fn () => $provider->invoke($invocation))
            ->toThrow(RuntimeException::class, 'Gateway companion process exceeded its output limit.');
    } finally {
        @unlink($script);
        @rmdir($directory);
    }
});
