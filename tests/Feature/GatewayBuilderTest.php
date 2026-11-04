<?php

declare(strict_types=1);

use Symfony\Component\Process\Process;

it('assembles a deterministic extension package through the standalone CLI', function (): void {
    $root = sys_get_temp_dir().'/gateway-builder-'.bin2hex(random_bytes(6));
    $source = $root.'/extension';
    $output = $root.'/output/example%2fdemo';
    mkdir($source.'/build', 0700, true);
    mkdir($source.'/runtime', 0700, true);
    file_put_contents($source.'/build/guest', 'guest-binary');
    file_put_contents($source.'/runtime/companion.php', '<?php echo "ok";');
    file_put_contents($source.'/extension.build.json', json_encode([
        'identity' => 'example/demo',
        'descriptor' => [
            'contracts' => [[
                'name' => 'gateway.automation',
                'version' => 'v1',
                'operations' => [['name' => 'execute', 'surface' => 'demo']],
            ]],
        ],
        'metadata' => [
            'example/v1' => ['version' => '{{version}}'],
        ],
        'artifacts' => [
            ['name' => 'guest', 'source' => 'build/guest', 'path' => 'bin/guest', 'executable' => true],
            ['name' => 'runtime', 'source' => 'runtime', 'path' => 'runtime', 'kind' => 'tree'],
        ],
        'entrypoints' => [[
            'name' => 'spinner',
            'runtime' => 'process',
            'artifact' => 'guest',
            'protocols' => ['ext.plugin/v1'],
        ]],
    ], JSON_PRETTY_PRINT | JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES));

    $process = new Process([
        PHP_BINARY,
        dirname(__DIR__, 2).'/mod/builder/bin/gy',
        'build',
        '--manifest='.$source.'/extension.build.json',
        '--version=1.2.3',
        '--revision=test-r1',
        '--output='.$output,
    ]);

    try {
        $process->mustRun();
        $manifest = json_decode((string) file_get_contents($output.'/plugin.json'), true, flags: JSON_THROW_ON_ERROR);

        expect(trim($process->getOutput()))->toBe($output)
            ->and(file_get_contents($output.'/bin/guest'))->toBe('guest-binary')
            ->and(file_get_contents($output.'/runtime/companion.php'))->toBe('<?php echo "ok";')
            ->and(fileperms($output.'/bin/guest') & 0111)->not->toBe(0)
            ->and($manifest['apiVersion'])->toBe('ext.package/v1')
            ->and($manifest['descriptor']['identity'])->toBe([
                'id' => 'example/demo',
                'revision' => 'test-r1',
                'version' => '1.2.3',
            ])
            ->and($manifest['metadata']['example/v1']['version'])->toBe('1.2.3')
            ->and($manifest['artifacts'][0]['sha256'])->toBe(hash('sha256', 'guest-binary'))
            ->and($manifest['artifacts'][1]['kind'])->toBe('tree');
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

it('exposes the same builder as a Composer plugin command', function (): void {
    $process = new Process(['composer', 'gy:build', '--help'], dirname(__DIR__, 2));
    $process->mustRun();

    expect($process->getOutput())
        ->toContain('Build a deterministic ext.package/v1 Gateway extension')
        ->toContain('--manifest=MANIFEST')
        ->toContain('--extension-version=EXTENSION-VERSION')
        ->toContain('--output=OUTPUT');
});

it('rejects symbolic-link artifact sources without leaving partial output', function (): void {
    $root = sys_get_temp_dir().'/gateway-builder-link-'.bin2hex(random_bytes(6));
    $source = $root.'/extension';
    $output = $root.'/output/example%2fdemo';
    mkdir($source, 0700, true);
    file_put_contents($source.'/actual', 'secret');
    symlink($source.'/actual', $source.'/linked');
    file_put_contents($source.'/extension.build.json', json_encode([
        'identity' => 'example/demo',
        'artifacts' => [
            ['name' => 'guest', 'source' => 'linked', 'path' => 'bin/guest'],
        ],
    ], JSON_THROW_ON_ERROR));

    $process = new Process([
        PHP_BINARY,
        dirname(__DIR__, 2).'/mod/builder/bin/gy',
        'build',
        '--manifest='.$source.'/extension.build.json',
        '--version=1.0.0',
        '--output='.$output,
    ]);

    try {
        $process->run();
        expect($process->isSuccessful())->toBeFalse()
            ->and($process->getErrorOutput())->toContain('symbolic link')
            ->and($output)->not->toBeDirectory();
    } finally {
        @unlink($source.'/linked');
        @unlink($source.'/actual');
        @unlink($source.'/extension.build.json');
        @rmdir($source);
        @rmdir($root.'/output');
        @rmdir($root);
    }
});
