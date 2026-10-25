<?php

declare(strict_types=1);

use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;
use Webong\Gateway\Automation\ExtAutomationPackageCatalog;
use Webong\Gateway\Companion\CompanionArtifactReconciler;
use Webong\Gateway\Companion\ExtCompanionPackageCatalog;
use Webong\Gateway\Companion\ExtensionArtifactInstaller;
use Webong\Gateway\Companion\ExtensionArtifactReconciler;
use Webong\Gateway\Companion\RegisteredCompanionActions;
use Webong\Gateway\Contracts\AutomationCapability;
use Webong\Gateway\Contracts\AutomationExecutionTarget;
use Webong\Gateway\Contracts\AutomationLanguageRegistry;
use Webong\Gateway\Contracts\CompanionActionRegistry;
use Webong\Gateway\Email\CredentialScriptRegistry;
use Webong\Gateway\Protocols\CompanionActionInvocation;

it('installs and activates a combined HookScript package after Planner has started', function (): void {
    $store = sys_get_temp_dir().'/gateway-planner-ext-store-'.bin2hex(random_bytes(6));
    $generations = sys_get_temp_dir().'/gateway-planner-ext-generations-'.bin2hex(random_bytes(6));
    [$archive, $cleanup] = hookScriptDistributionArchive();
    $url = 'https://extensions.example.test/webong-hookscript-1.0.0.tar.gz';
    Http::fake([$url => Http::response($archive, 200)]);
    config()->set('gateway.companion_actions.plugin_dir', $store);
    config()->set('gateway.companion_actions.generation_root', $generations);

    try {
        $created = $this->withToken('registry-secret')->postJson('/extensions', [
            'extension' => 'webong/hookscript',
            'enabled' => true,
            'mode' => 'dynamic',
            'version' => '1.0.0',
            'artifact' => ['url' => $url, 'sha256' => hash('sha256', $archive)],
        ])->assertCreated();

        app()->forgetInstance(CompanionActionRegistry::class);
        app()->forgetInstance(RegisteredCompanionActions::class);
        app()->forgetInstance(ExtCompanionPackageCatalog::class);
        app()->forgetInstance(ExtensionArtifactInstaller::class);
        app()->forgetInstance(CompanionArtifactReconciler::class);

        $this->withHeader('X-Gateway-Internal', 'internal-secret')
            ->getJson('/_internal/gateway/companion-actions')
            ->assertOk()
            ->assertJsonPath('actions.0.name', 'gateway.hookscript.execute')
            ->assertJsonPath('actions.0.mode', 'dynamic');

        $registry = app(CompanionActionRegistry::class);
        $result = $registry->invoke(new CompanionActionInvocation(
            action: 'gateway.hookscript.execute',
            executionId: 'post-deploy-hookscript',
            idempotencyKey: 'post-deploy-hookscript',
            input: [
                'source' => "respond('installed', 202);",
                'event' => [
                    'delivery_id' => 'delivery-post-deploy', 'method' => 'POST',
                    'host' => 'hooks.example.test', 'path' => '/installed',
                    'raw_query' => '', 'headers' => [], 'body' => '',
                ],
                'variables' => [],
            ],
            deadline: new DateTimeImmutable('+30 seconds'),
            companionVersion: '1.0.0',
        ));

        expect($result->output['outcome']['response'])->toBe([
            'status' => 202, 'headers' => [], 'body' => 'installed',
        ]);

        $this->withToken('registry-secret')->getJson('/extensions')
            ->assertOk()
            ->assertJsonFragment([
                'extension' => 'webong/hookscript', 'state' => 'ready', 'mode' => 'dynamic',
            ]);
        expect($created->json('revision'))->toBe(1);
    } finally {
        $cleanup();
        removeDistributionDirectory($store);
        removeDistributionDirectory($generations);
    }
});

it('fails closed before Planner discovers a package with the wrong artifact digest', function (): void {
    $store = sys_get_temp_dir().'/gateway-planner-ext-store-'.bin2hex(random_bytes(6));
    $url = 'https://extensions.example.test/tampered.tar.gz';
    Http::fake([$url => Http::response('tampered archive', 200)]);
    config()->set('gateway.companion_actions.plugin_dir', $store);
    config()->set('gateway.companion_actions.generation_root', $store.'-generations');

    try {
        $this->withToken('registry-secret')->postJson('/extensions', [
            'extension' => 'webong/hookscript',
            'enabled' => true,
            'mode' => 'dynamic',
            'version' => '1.0.0',
            'artifact' => ['url' => $url, 'sha256' => str_repeat('0', 64)],
        ])->assertCreated();
        app()->forgetInstance(CompanionActionRegistry::class);
        app()->forgetInstance(RegisteredCompanionActions::class);
        app()->forgetInstance(ExtCompanionPackageCatalog::class);
        app()->forgetInstance(ExtensionArtifactInstaller::class);
        app()->forgetInstance(CompanionArtifactReconciler::class);

        $this->withHeader('X-Gateway-Internal', 'internal-secret')
            ->getJson('/_internal/gateway/companion-actions')
            ->assertServiceUnavailable()
            ->assertJsonPath('message', 'Companion extension artifacts are unavailable.');
        expect(is_dir($store.'/webong%2fhookscript'))->toBeFalse();
    } finally {
        removeDistributionDirectory($store);
        removeDistributionDirectory($store.'-generations');
    }
});

it('reports an unavailable extension catalog when its artifact origin cannot be reached', function (): void {
    $store = sys_get_temp_dir().'/gateway-planner-ext-store-'.bin2hex(random_bytes(6));
    $url = 'https://extensions.example.test/unavailable.tar.gz';
    Http::fake(static function () use ($url): never {
        throw new ConnectionException("Cannot reach {$url}");
    });
    config()->set('gateway.companion_actions.plugin_dir', $store);

    try {
        $this->withToken('registry-secret')->postJson('/extensions', [
            'extension' => 'example/unavailable',
            'enabled' => true,
            'mode' => 'dynamic',
            'version' => '1.0.0',
            'artifact' => ['url' => $url, 'sha256' => str_repeat('0', 64)],
        ])->assertCreated();

        foreach ([ExtAutomationPackageCatalog::class, ExtensionArtifactInstaller::class, ExtensionArtifactReconciler::class] as $binding) {
            app()->forgetInstance($binding);
        }

        $this->withToken('registry-secret')->getJson('/extensions')
            ->assertServiceUnavailable()
            ->assertJsonPath('message', 'Gateway extension artifacts are unavailable.');
        expect(is_dir($store.'/example%2funavailable'))->toBeFalse();
    } finally {
        removeDistributionDirectory($store);
    }
});

it('discovers automation capabilities from an installed package without a Planner companion', function (): void {
    $store = sys_get_temp_dir().'/gateway-planner-automation-store-'.bin2hex(random_bytes(6));
    [$archive, $cleanup] = automationDistributionArchive();
    $url = 'https://extensions.example.test/example-dynamic-script-1.0.0.tar.gz';
    Http::fake([
        $url => Http::response($archive, 200),
        'http://spinner.internal:5052/automation/credential-hook' => Http::response([
            'delivery' => [
                'url' => 'https://credentials.example.test/exchange',
                'method' => 'POST',
                'body' => '{"installed":true}',
            ],
        ]),
    ]);
    config()->set('gateway.companion_actions.plugin_dir', $store);
    config()->set('gateway.internal_token', 'internal-secret');
    config()->set('gateway.delivery_admin_url', 'http://spinner.internal:5052');

    try {
        $this->withToken('registry-secret')->postJson('/extensions', [
            'extension' => 'example/dynamic-script',
            'enabled' => true,
            'mode' => 'dynamic',
            'version' => '1.0.0',
            'artifact' => ['url' => $url, 'sha256' => hash('sha256', $archive)],
        ])->assertCreated();

        foreach ([
            AutomationLanguageRegistry::class,
            ExtAutomationPackageCatalog::class,
            ExtensionArtifactInstaller::class,
            ExtensionArtifactReconciler::class,
        ] as $binding) {
            app()->forgetInstance($binding);
        }

        $languages = app(AutomationLanguageRegistry::class);
        expect($languages->supports('dynamic-script', AutomationCapability::ROUTE))->toBeTrue()
            ->and($languages->supports('dynamic-script', AutomationCapability::SCHEDULE))->toBeTrue()
            ->and($languages->supports('dynamic-script', AutomationCapability::CREDENTIAL))->toBeTrue()
            ->and($languages->target('dynamic-script'))->toBe(AutomationExecutionTarget::SPINNER)
            ->and(is_dir($store.'/example%2fdynamic-script'))->toBeTrue();

        $credentials = app(CredentialScriptRegistry::class);
        expect($credentials->supports('dynamic-script'))->toBeTrue()
            ->and($credentials->execute('dynamic-script', 'script source', ['protocol' => 'imap'])->body)
            ->toBe('{"installed":true}');
        $this->withToken('registry-secret')->getJson('/extensions')
            ->assertOk()
            ->assertJsonFragment([
                'extension' => 'example/dynamic-script',
                'state' => 'ready',
                'automations' => [[
                    'id' => 'dynamic-script',
                    'target' => 'spinner',
                    'capabilities' => ['route', 'schedule', 'credential'],
                ]],
            ]);

        file_put_contents($store.'/example%2fdynamic-script/bin/spinner', 'tampered automation guest');
        app()->forgetInstance(AutomationLanguageRegistry::class);
        app()->forgetInstance(ExtAutomationPackageCatalog::class);
        expect(fn (): bool => app(AutomationLanguageRegistry::class)
            ->supports('dynamic-script', AutomationCapability::ROUTE))
            ->toThrow(RuntimeException::class, 'integrity verification');
        $this->withToken('registry-secret')->getJson('/extensions')
            ->assertServiceUnavailable()
            ->assertJsonPath('message', 'Gateway extension artifacts are unavailable.');
    } finally {
        $cleanup();
        removeDistributionDirectory($store);
    }
});

/** @return array{string, Closure(): void} */
function hookScriptDistributionArchive(): array
{
    $project = dirname(__DIR__, 2);
    $root = sys_get_temp_dir().'/gateway-hookscript-distribution-'.bin2hex(random_bytes(6));
    $package = $root.'/webong%2fhookscript';
    mkdir($package.'/bin', 0700, true);
    copy($project.'/pkg/hookscript/bin/companion', $package.'/bin/companion');
    file_put_contents($package.'/bin/spinner', 'Spinner selects this artifact on its own host.');
    copyDistributionTree($project.'/pkg/hookscript/src', $package.'/src');
    copyDistributionTree($project.'/pkg/hookscript/companion/vendor', $package.'/vendor');

    $identity = ['id' => 'webong/hookscript', 'revision' => 'post-deploy-r1', 'version' => '1.0.0'];
    $companion = [
        'apiVersion' => 'ext.plugin/v1', 'identity' => $identity,
        'contracts' => [[
            'name' => 'gateway.companion', 'version' => 'v1',
            'operations' => [['name' => 'gateway.hookscript.execute']],
        ]],
    ];
    $manifest = [
        'apiVersion' => 'ext.package/v1',
        'descriptor' => [
            'apiVersion' => 'ext.plugin/v1', 'identity' => $identity,
            'contracts' => [[
                'name' => 'gateway.automation', 'version' => 'v1',
                'operations' => [['name' => 'execute', 'surface' => 'webhookscript']],
            ]],
            'reverse' => [[
                'name' => 'gateway.companion', 'version' => 'v1',
                'operations' => [['name' => 'gateway.hookscript.execute']],
            ]],
        ],
        'metadata' => ['gateway.companion/v1' => ['descriptor' => $companion]],
        'artifacts' => [
            ['name' => 'spinner-guest', 'path' => 'bin/spinner', 'sha256' => hash_file('sha256', $package.'/bin/spinner')],
            ['name' => 'planner-companion', 'path' => 'bin/companion', 'sha256' => hash_file('sha256', $package.'/bin/companion')],
            ['name' => 'source', 'path' => 'src', 'kind' => 'tree', 'sha256' => distributionTreeDigest($package.'/src')],
            ['name' => 'vendor', 'path' => 'vendor', 'kind' => 'tree', 'sha256' => distributionTreeDigest($package.'/vendor')],
        ],
        'entrypoints' => [
            ['name' => 'spinner', 'runtime' => 'process', 'artifact' => 'spinner-guest', 'protocols' => ['ext.plugin/v1']],
            ['name' => 'planner', 'runtime' => 'php-jsonline', 'artifact' => 'planner-companion', 'protocols' => ['ext.plugin/v1']],
        ],
    ];
    file_put_contents($package.'/plugin.json', json_encode($manifest, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES));
    $tar = $root.'/hookscript.tar';
    $phar = new PharData($tar);
    $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($package, FilesystemIterator::SKIP_DOTS));
    foreach ($iterator as $file) {
        if ($file->isFile()) {
            $relative = 'webong%2fhookscript/'.str_replace('\\', '/', substr($file->getPathname(), strlen($package) + 1));
            $phar->addFile($file->getPathname(), $relative);
        }
    }
    $compressed = $phar->compress(Phar::GZ);
    unset($compressed);
    unset($phar);
    $archive = file_get_contents($tar.'.gz');

    return [$archive, static function () use ($root): void { removeDistributionDirectory($root); }];
}

/** @return array{string, Closure(): void} */
function automationDistributionArchive(): array
{
    $root = sys_get_temp_dir().'/gateway-automation-distribution-'.bin2hex(random_bytes(6));
    $package = $root.'/example%2fdynamic-script';
    mkdir($package.'/bin', 0700, true);
    file_put_contents($package.'/bin/spinner', 'test automation guest');
    $identity = ['id' => 'example/dynamic-script', 'revision' => 'dynamic-r1', 'version' => '1.0.0'];
    $manifest = [
        'apiVersion' => 'ext.package/v1',
        'descriptor' => [
            'apiVersion' => 'ext.plugin/v1', 'identity' => $identity,
            'contracts' => [[
                'name' => 'gateway.automation', 'version' => 'v1',
                'operations' => [['name' => 'execute', 'surface' => 'dynamic-script']],
            ]],
        ],
        'metadata' => ['gateway.automation/v1' => ['languages' => [[
            'id' => 'dynamic-script', 'target' => 'spinner', 'capabilities' => ['route', 'schedule', 'credential'],
        ]]]],
        'artifacts' => [[
            'name' => 'spinner-guest', 'path' => 'bin/spinner',
            'sha256' => hash_file('sha256', $package.'/bin/spinner'),
        ]],
        'entrypoints' => [[
            'name' => 'spinner', 'runtime' => 'process', 'artifact' => 'spinner-guest',
            'protocols' => ['ext.plugin/v1'],
        ]],
    ];
    file_put_contents($package.'/plugin.json', json_encode($manifest, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES));
    $tar = $root.'/automation.tar';
    $phar = new PharData($tar);
    $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($package, FilesystemIterator::SKIP_DOTS));
    foreach ($iterator as $file) {
        if ($file->isFile()) {
            $relative = 'example%2fdynamic-script/'.str_replace('\\', '/', substr($file->getPathname(), strlen($package) + 1));
            $phar->addFile($file->getPathname(), $relative);
        }
    }
    $phar->compress(Phar::GZ);
    unset($phar);
    $archive = file_get_contents($tar.'.gz');

    return [$archive, static function () use ($root): void { removeDistributionDirectory($root); }];
}

function copyDistributionTree(string $source, string $destination): void
{
    mkdir($destination, 0700, true);
    $iterator = new RecursiveIteratorIterator(
        new RecursiveDirectoryIterator($source, FilesystemIterator::SKIP_DOTS),
        RecursiveIteratorIterator::SELF_FIRST,
    );
    foreach ($iterator as $entry) {
        $relative = substr($entry->getPathname(), strlen($source) + 1);
        $target = $destination.DIRECTORY_SEPARATOR.$relative;
        $entry->isDir() ? (is_dir($target) || mkdir($target, 0700, true)) : copy($entry->getPathname(), $target);
    }
}

function distributionTreeDigest(string $directory): string
{
    $files = [];
    $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS));
    foreach ($iterator as $file) {
        $files[] = str_replace('\\', '/', substr($file->getPathname(), strlen($directory) + 1));
    }
    sort($files, SORT_STRING);
    $context = hash_init('sha256');
    foreach ($files as $relative) {
        hash_update($context, './'.$relative.' '.hash_file('sha256', $directory.'/'.$relative)."\n");
    }

    return hash_final($context);
}

function removeDistributionDirectory(?string $directory): void
{
    if ($directory === null || ! is_dir($directory)) {
        return;
    }
    $iterator = new RecursiveIteratorIterator(
        new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS),
        RecursiveIteratorIterator::CHILD_FIRST,
    );
    foreach ($iterator as $entry) {
        @chmod($entry->getPathname(), $entry->isDir() ? 0700 : 0600);
        $entry->isDir() ? @rmdir($entry->getPathname()) : @unlink($entry->getPathname());
    }
    @rmdir($directory);
}
