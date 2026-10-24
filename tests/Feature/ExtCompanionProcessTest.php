<?php

declare(strict_types=1);

use Webong\Gateway\Companion\CompanionActionActivationStore;
use Webong\Gateway\Companion\CompanionExecutionPolicy;
use Webong\Gateway\Companion\ExtCompanionPackageCatalog;
use Webong\Gateway\Companion\ExtProcessCompanionActionProvider;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Protocols\CompanionActionCapability;
use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;
use Symfony\Component\Process\Process;

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

it('reuses one bounded warmed process and retires it when policy changes', function (): void {
    $projectRoot = dirname(__DIR__, 2);
    $directory = sys_get_temp_dir().'/gateway-warmed-ext-'.bin2hex(random_bytes(6));
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
            private int $calls = 0;
            public function handle(CompanionActionInvocation $invocation): CompanionActionResult {
                if ($invocation->input['crash'] ?? false) {
                    exit(9);
                }
                $this->calls++;
                return CompanionActionResult::succeeded($invocation->executionId, [
                    'calls' => $this->calls,
                    'pid' => getmypid(),
                ]);
            }
        };
        exit((new CompanionProcessGuest(
            getenv('GATEWAY_EXT_ID') ?: '', getenv('GATEWAY_EXT_REVISION') ?: '', '1.0.0',
            ['test.warmed' => $action],
        ))->serve());
        PHP;
    file_put_contents($directory.'/companion.php', str_replace('__AUTOLOAD__', $autoload, $script));
    $state = (object) ['policy' => new CompanionExecutionPolicy(
        mode: CompanionExecutionPolicy::WARMED,
        maxInvocations: 2,
        generation: 1,
    )];
    $provider = new ExtProcessCompanionActionProvider(
        extensionId: 'example/warmed',
        revision: 'test-r1',
        providedCapability: new CompanionActionCapability('test.warmed', '1.0.0'),
        descriptor: extDescriptor('example/warmed', 'test.warmed'),
        executable: $directory.'/companion.php',
        workingDirectory: $directory,
        maxRuntimeSeconds: 10,
        executionPolicy: fn (): CompanionExecutionPolicy => $state->policy,
    );

    try {
        $first = $provider->invoke(processInvocation('test.warmed', 'warmed-1', []))->output;
        $second = $provider->invoke(processInvocation('test.warmed', 'warmed-2', []))->output;
        $third = $provider->invoke(processInvocation('test.warmed', 'warmed-3', []))->output;

        expect($first['calls'])->toBe(1)
            ->and($second['calls'])->toBe(2)
            ->and($second['pid'])->toBe($first['pid'])
            ->and($third['calls'])->toBe(1)
            ->and($third['pid'])->not->toBe($first['pid']);

        $state->policy = new CompanionExecutionPolicy(
            mode: CompanionExecutionPolicy::WARMED,
            maxInvocations: 2,
            generation: 2,
        );
        $reconfigured = $provider->invoke(processInvocation('test.warmed', 'warmed-4', []))->output;
        expect($reconfigured['calls'])->toBe(1)
            ->and($reconfigured['pid'])->not->toBe($third['pid']);

        expect(fn () => $provider->invoke(processInvocation('test.warmed', 'warmed-crash', ['crash' => true])))
            ->toThrow(RuntimeException::class, 'Gateway companion process failed.');
        expect($provider->invoke(processInvocation('test.warmed', 'warmed-recovered', []))->output['calls'])
            ->toBe(1);

        $state->policy = new CompanionExecutionPolicy(generation: 3);
        expect($provider->invoke(processInvocation('test.warmed', 'isolated-1', []))->output['calls'])->toBe(1)
            ->and($provider->invoke(processInvocation('test.warmed', 'isolated-2', []))->output['calls'])->toBe(1);
    } finally {
        unset($provider);
        @unlink($directory.'/companion.php');
        @rmdir($directory);
    }
});

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
            private int $calls = 0;
            public function handle(CompanionActionInvocation $invocation): CompanionActionResult {
                $this->calls++;
                return CompanionActionResult::succeeded($invocation->executionId, [
                    'provider' => 'ext-process', 'subject' => $invocation->input['subject'] ?? null,
                    'parent_secret' => getenv('GATEWAY_TEST_PARENT_SECRET'),
                    'calls' => $this->calls, 'pid' => getmypid(),
                ]);
            }
        };
        $version = getenv('GATEWAY_EXT_VERSION') ?: null;
        exit((new CompanionProcessGuest(
            getenv('GATEWAY_EXT_ID') ?: '', getenv('GATEWAY_EXT_REVISION') ?: '', $version,
            ['test.dynamic-ticket' => $action, 'test.dynamic-ticket-followup' => $action],
        ))->serve());
        PHP;
    $script = str_replace('__AUTOLOAD__', $autoload, $script);
    file_put_contents($directory.'/companion.php', $script);
    $descriptor = extDescriptor('example/dynamic-ticket', 'test.dynamic-ticket');
    $descriptor['contracts'][0]['operations'][] = ['name' => 'test.dynamic-ticket-followup'];
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
        $providers = (new ExtCompanionPackageCatalog(
            $root,
            10,
            executionPolicy: static fn (): CompanionExecutionPolicy => new CompanionExecutionPolicy(
                mode: CompanionExecutionPolicy::WARMED,
            ),
        ))->providers();
        expect($providers)->toHaveCount(2);
        $registry = app(CompanionActionRegistry::class);
        foreach ($providers as $provider) {
            $registry->registerProvider($provider);
        }
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
        $followup = $registry->invoke(processInvocation(
            'test.dynamic-ticket-followup',
            'dynamic-ticket-followup-1',
            ['subject' => 'Same package'],
        ));
        expect($registry->capability('test.dynamic-ticket')?->mode)->toBe('dynamic')
            ->and($result->output)->toBe([
                'provider' => 'ext-process',
                'subject' => 'Installed once',
                'parent_secret' => false,
                'calls' => 1,
                'pid' => $result->output['pid'],
            ])
            ->and($followup->output['calls'])->toBe(2)
            ->and($followup->output['pid'])->toBe($result->output['pid']);

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

it('builds a combined HookScript package that Planner verifies', function (): void {
    $projectRoot = dirname(__DIR__, 2);
    $root = sys_get_temp_dir().'/gateway-built-ext-'.bin2hex(random_bytes(6));
    $vendor = $root.'/fixture-vendor';
    $destination = $root.'/store/webong%2fhookscript';
    mkdir($vendor, 0700, true);
    file_put_contents($vendor.'/autoload.php', "<?php\n");
    $build = new Process(
        [PHP_BINARY, $projectRoot.'/pkg/hookscript/scripts/build-ext-package', $destination, '1.0.0', 'test-builder-r1'],
        $projectRoot.'/pkg/hookscript',
        ['GATEWAY_HOOKSCRIPT_VENDOR_DIR' => $vendor],
        timeout: 120,
    );

    try {
        $build->mustRun();
        $manifest = json_decode((string) file_get_contents($destination.'/plugin.json'), true, 128, JSON_THROW_ON_ERROR);
        expect(array_column($manifest['entrypoints'], 'name'))->toBe(['spinner', 'planner'])
            ->and($manifest['descriptor']['contracts'][0]['name'])->toBe('gateway.automation')
            ->and($manifest['metadata']['gateway.companion/v1']['descriptor']['contracts'][0]['name'])
            ->toBe('gateway.companion')
            ->and((new ExtCompanionPackageCatalog($root.'/store', 10))->providers())->toHaveCount(1);
    } finally {
        if (is_dir($root)) {
            $iterator = new RecursiveIteratorIterator(
                new RecursiveDirectoryIterator($root, FilesystemIterator::SKIP_DOTS),
                RecursiveIteratorIterator::CHILD_FIRST,
            );
            foreach ($iterator as $entry) {
                $entry->isDir() ? @rmdir($entry->getPathname()) : @unlink($entry->getPathname());
            }
            @rmdir($root);
        }
    }
});
