#!/usr/bin/env php
<?php

declare(strict_types=1);

$root = dirname(__DIR__);
$pin = json_decode(file_get_contents($root.'/tools/proxy/source.json'), true, flags: JSON_THROW_ON_ERROR);
$destination = $root.'/app/ext/proxy';
$marker = $destination.'/.gateway-source.json';
$treeHash = static function (string $directory): string {
    $files = [];
    foreach (new RecursiveIteratorIterator(new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS)) as $file) {
        $relative = substr($file->getPathname(), strlen($directory) + 1);
        if ($relative === '.gateway-source.json') {
            continue;
        }
        if ($file->isLink()) {
            throw new RuntimeException('Proxy source must not contain symlinks.');
        }
        if ($file->isFile()) {
            $files[$relative] = hash_file('sha256', $file->getPathname());
        }
    }
    ksort($files);
    return hash('sha256', json_encode($files, JSON_THROW_ON_ERROR));
};
if (is_dir($destination)) {
    $installed = is_file($marker) ? json_decode(file_get_contents($marker), true, flags: JSON_THROW_ON_ERROR) : [];
    if (($installed['commit'] ?? null) !== $pin['commit'] || ($installed['archive_sha256'] ?? null) !== $pin['sha256']
        || ($installed['tree_sha256'] ?? null) !== $treeHash($destination)) {
        throw new RuntimeException('Existing app/ext/proxy differs from its pin. Preserve local edits and explicitly remove/reprovision it before installing.');
    }
    echo 'Proxy source verified at '.$pin['commit']."\n";
    exit(0);
}
$temporary = sys_get_temp_dir().'/gateway-proxy-'.bin2hex(random_bytes(8));
mkdir($temporary, 0700);
try {
    $archive = getenv('GATEWAY_PROXY_ARCHIVE') ?: $pin['archive'];
    $bytes = file_get_contents($archive, false, stream_context_create(['http' => ['timeout' => 120]]));
    if ($bytes === false || ! hash_equals($pin['sha256'], hash('sha256', $bytes))) {
        throw new RuntimeException('Proxy archive checksum mismatch.');
    }
    file_put_contents($temporary.'/proxy.tar.gz', $bytes);
    (new PharData($temporary.'/proxy.tar.gz'))->extractTo($temporary.'/source');
    $source = $temporary.'/source/proxy-'.$pin['commit'];
    foreach (['composer.json' => 'webong/proxy', 'adapters/laravel/composer.json' => 'webong/proxy-laravel'] as $path => $name) {
        $manifest = json_decode(file_get_contents($source.'/'.$path), true, flags: JSON_THROW_ON_ERROR);
        if (($manifest['name'] ?? null) !== $name || ($manifest['version'] ?? null) !== '0.4.0') {
            throw new RuntimeException('Unexpected pinned Proxy manifest: '.$path);
        }
    }
    file_put_contents($source.'/.gateway-source.json', json_encode(['commit' => $pin['commit'], 'archive_sha256' => $pin['sha256'], 'tree_sha256' => $treeHash($source)], JSON_PRETTY_PRINT)."\n");
    if (! is_dir(dirname($destination))) {
        mkdir(dirname($destination), 0755, true);
    }
    if (! rename($source, $destination)) {
        throw new RuntimeException('Could not provision Proxy checkout.');
    }
    echo 'Provisioned Proxy '.$pin['commit']."\n";
} finally {
    $files = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($temporary, FilesystemIterator::SKIP_DOTS), RecursiveIteratorIterator::CHILD_FIRST);
    foreach ($files as $file) {
        $file->isDir() ? rmdir($file->getPathname()) : unlink($file->getPathname());
    }
    rmdir($temporary);
}
