<?php

declare(strict_types=1);

use Webong\Gateway\Companion\CompanionActionActivationStore;
use Webong\Gateway\Companion\ExtCompanionPackageCatalog;
use Webong\Gateway\Companion\ExtProcessCompanionActionProvider;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

function extDescriptor(string $id, string $action, string $revision = 'test-r1'): array
{
    return [
        'apiVersion' => 'ext.plugin/v1',
        'identity' => ['id' => $id, 'revision' => $revision, 'version' => '1.0.0'],
        'contracts' => [[
            'name' => 'gateway.companion',
            'version' => 'v1',
            'operations' => [['name' => $action]],
        ]],
    ];
}

function processInvocation(string $action, string $execution, array $input): CompanionActionInvocation
{
    return new CompanionActionInvocation(
        action: $action,
        executionId: $execution,
        idempotencyKey: $execution,
        input: $input,
        deadline: new DateTimeImmutable('+30 seconds'),
        companionVersion: '1.0.0',
    );
}

it('runs the HookScript Composer companion as an ext PHP process', function (): void {
    $projectRoot = dirname(__DIR__, 2);
    $descriptor = extDescriptor('webong/hookscript', 'gateway.hookscript.execute');
    $provider = new ExtProcessCompanionActionProvider(
        extensionId: 'webong/hookscript',
        revision: 'test-r1',
        providedCapability: new CompanionActionCapability('gateway.hookscript.execute', '1.0.0'),
        descriptor: $descriptor,
        executable: $projectRoot.'/pkg/hookscript/bin/companion',
        workingDirectory: $projectRoot.'/pkg/hookscript',
        maxRuntimeSeconds: 10,
    );

    $result = $provider->invoke(processInvocation('gateway.hookscript.execute', 'dynamic-hookscript-1', [
        'source' => "respond('dynamic', 203, ['X-Companion: process']);",
        'event' => [
            'delivery_id' => 'delivery-dynamic',
            'method' => 'POST',
            'host' => 'hooks.example.test',
            'path' => '/dynamic',
            'raw_query' => '',
            'headers' => [],
            'body' => 'payload',
        ],
        'variables' => [],
    ]));

    expect($result->status)->toBe(CompanionActionResult::SUCCEEDED)
        ->and($result->output['outcome']['response'])->toBe([
            'status' => 203,
            'headers' => ['X-Companion' => ['process']],
            'body' => 'dynamic',
        ]);
});

it('discovers a verified ext package and activates its dynamic Planner process', function (): void {
    $projectRoot = dirname(__DIR__, 2);
    $root = sys_get_temp_dir().'/gateway-ext-'.bin2hex(random_bytes(6));
    $directory = $root.'/example%2fdynamic-ticket';
    mkdir($directory, 0700, true);
    $autoload = var_export($projectRoot.'/vendor/autoload.php', true);
    $script = <<<'PHP'
        <?php
        declare(strict_types=1);
        require __AUTOLOAD__;
        use Webong\Gateway\Companion\CompanionProcessGuest;
        use Webong\Gateway\Contracts\CompanionAction;
        use Webong\Gateway\Protocols\CompanionActionInvocation;
        use Webong\Gateway\Protocols\CompanionActionResult;
        $action = new class implements CompanionAction {
            public function handle(CompanionActionInvocation $invocation): CompanionActionResult {
                return CompanionActionResult::succeeded($invocation->executionId, [
                    'provider' => 'ext-process', 'subject' => $invocation->input['subject'] ?? null,
                    'parent_secret' => getenv('GATEWAY_TEST_PARENT_SECRET'),
                ]);
            }
        };
        $version = getenv('GATEWAY_EXT_VERSION') ?: null;
        exit((new CompanionProcessGuest(
            getenv('GATEWAY_EXT_ID') ?: '', getenv('GATEWAY_EXT_REVISION') ?: '', $version,
            ['test.dynamic-ticket' => $action],
        ))->serve());
        PHP;
    $script = str_replace('__AUTOLOAD__', $autoload, $script);
    file_put_contents($directory.'/companion.php', $script);
    $descriptor = extDescriptor('example/dynamic-ticket', 'test.dynamic-ticket');
    $spinnerDescriptor = [
        'apiVersion' => 'ext.plugin/v1',
        'identity' => $descriptor['identity'],
        'contracts' => [[
            'name' => 'gateway.automation',
            'version' => 'v1',
            'operations' => [['name' => 'execute', 'surface' => 'dynamic-ticket']],
        ]],
    ];
    file_put_contents($directory.'/spinner', 'test spinner artifact');
    $manifest = [
        'apiVersion' => 'ext.package/v1',
        'descriptor' => $spinnerDescriptor,
        'metadata' => ['gateway.companion/v1' => ['descriptor' => $descriptor]],
        'artifacts' => [
            [
                'name' => 'spinner-guest',
                'path' => 'spinner',
                'sha256' => hash_file('sha256', $directory.'/spinner'),
            ],
            [
                'name' => 'planner-companion',
                'path' => 'companion.php',
                'sha256' => hash_file('sha256', $directory.'/companion.php'),
            ],
        ],
        'entrypoints' => [
            [
                'name' => 'spinner',
                'runtime' => 'process',
                'artifact' => 'spinner-guest',
                'protocols' => ['ext.plugin/v1'],
            ],
            [
                'name' => 'planner',
                'runtime' => 'php-jsonline',
                'artifact' => 'planner-companion',
                'protocols' => ['ext.plugin/v1'],
            ],
        ],
    ];
    file_put_contents($directory.'/plugin.json', json_encode($manifest, JSON_THROW_ON_ERROR));
    putenv('GATEWAY_TEST_PARENT_SECRET=must-not-leak');
    $_ENV['GATEWAY_TEST_PARENT_SECRET'] = 'must-not-leak';
    $_SERVER['GATEWAY_TEST_PARENT_SECRET'] = 'must-not-leak';

    try {
        $providers = (new ExtCompanionPackageCatalog($root, 10))->providers();
        expect($providers)->toHaveCount(1);
        $registry = app(CompanionActionRegistry::class);
        $registry->registerProvider($providers[0]);
        app(CompanionActionActivationStore::class)->create([
            'extension' => 'example/dynamic-ticket',
            'mode' => 'dynamic',
            'version' => '1.0.0',
        ]);

        $result = $registry->invoke(processInvocation(
            'test.dynamic-ticket',
            'dynamic-ticket-1',
            ['subject' => 'Installed once'],
        ));
        expect($registry->capability('test.dynamic-ticket')?->mode)->toBe('dynamic')
            ->and($result->output)->toBe([
                'provider' => 'ext-process',
                'subject' => 'Installed once',
                'parent_secret' => false,
            ]);

        file_put_contents($directory.'/companion.php', "\n// changed after discovery\n", FILE_APPEND);
        expect(fn () => $registry->invoke(processInvocation(
            'test.dynamic-ticket',
            'dynamic-ticket-2',
            ['subject' => 'Must not run'],
        )))->toThrow(RuntimeException::class, 'Ext companion package changed after discovery.');
    } finally {
        putenv('GATEWAY_TEST_PARENT_SECRET');
        unset($_ENV['GATEWAY_TEST_PARENT_SECRET'], $_SERVER['GATEWAY_TEST_PARENT_SECRET']);
        @unlink($directory.'/plugin.json');
        @unlink($directory.'/companion.php');
        @unlink($directory.'/spinner');
        @rmdir($directory);
        @rmdir($root);
    }
});

it('rejects a modified ext companion artifact before registration', function (): void {
    $root = sys_get_temp_dir().'/gateway-ext-'.bin2hex(random_bytes(6));
    $directory = $root.'/example%2ftampered';
    mkdir($directory, 0700, true);
    file_put_contents($directory.'/companion.php', '<?php exit(0);');
    $manifest = [
        'apiVersion' => 'ext.package/v1',
        'descriptor' => extDescriptor('example/tampered', 'test.tampered'),
        'artifacts' => [[
            'name' => 'planner-companion',
            'path' => 'companion.php',
            'sha256' => str_repeat('0', 64),
        ]],
        'entrypoints' => [[
            'name' => 'planner', 'runtime' => 'php-jsonline',
            'artifact' => 'planner-companion', 'protocols' => ['ext.plugin/v1'],
        ]],
    ];
    file_put_contents($directory.'/plugin.json', json_encode($manifest, JSON_THROW_ON_ERROR));

    try {
        expect(fn () => (new ExtCompanionPackageCatalog($root, 10))->providers())
            ->toThrow(RuntimeException::class);
    } finally {
        @unlink($directory.'/plugin.json');
        @unlink($directory.'/companion.php');
        @rmdir($directory);
        @rmdir($root);
    }
});
