<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Http;
use Webong\Gateway\Companion\CompanionArtifactReconciler;
use Webong\Gateway\Companion\ExtCompanionPackageCatalog;
use Webong\Gateway\Companion\ExtensionArtifactInstaller;
use Webong\Gateway\Companion\RegisteredCompanionActions;
use Webong\Gateway\Contracts\CompanionActionRegistry;
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
