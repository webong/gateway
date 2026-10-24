<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Closure;
use FilesystemIterator;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use RuntimeException;
use Webong\Gateway\Contracts\CompanionActionProvider;
use Webong\Gateway\Protocols\CompanionActionCapability;

/** Discovers verified ext.package/v1 PHP companion entrypoints from one store. */
final readonly class ExtCompanionPackageCatalog
{
    private const string PACKAGE_VERSION = 'ext.package/v1';

    private const string PLUGIN_VERSION = 'ext.plugin/v1';

    private const string COMPANION_METADATA = 'gateway.companion/v1';

    public function __construct(
        private string $root,
        private int $maxRuntimeSeconds,
        private int $maxOutputBytes = 1048576,
        private string $phpBinary = PHP_BINARY,
        private ?Closure $executionPolicy = null,
    ) {
        if (trim($this->phpBinary) === '') {
            throw new RuntimeException('Gateway dynamic companions require a PHP CLI binary.');
        }
    }

    /** @return list<CompanionActionProvider> */
    public function providers(): array
    {
        if (trim($this->root) === '' || ! is_dir($this->root)) {
            return [];
        }
        $entries = array_values(array_filter(scandir($this->root) ?: [],
            static fn (string $name): bool => $name !== '.' && $name !== '..' && ! str_starts_with($name, '.')));
        sort($entries, SORT_STRING);
        $providers = [];
        foreach ($entries as $entry) {
            $directory = $this->root.DIRECTORY_SEPARATOR.$entry;
            if (! is_dir($directory) || is_link($directory)) {
                throw new RuntimeException("Invalid ext package store entry [{$entry}].");
            }
            $manifest = $this->manifest($directory);
            $identity = $manifest['descriptor']['identity'];
            if (! hash_equals(str_replace('%2f', '/', $entry), $identity['id'])) {
                throw new RuntimeException("Ext package directory [{$entry}] does not match its identity.");
            }
            $descriptor = $this->companionDescriptor($manifest);
            $actions = $this->companionActions($descriptor);
            if ($actions === []) {
                continue;
            }
            [$entrypoint, $artifacts] = $this->entrypoint($manifest);
            $this->verifyPackage($directory, $manifest, $artifacts);
            $executable = $this->securePath($directory, $artifacts[$entrypoint['artifact']]['path'], false);
            $digest = $this->directoryDigest($directory);
            $warmedScope = new WarmedCompanionProcessScope;
            foreach ($actions as $action) {
                $providers[] = new ExtProcessCompanionActionProvider(
                    extensionId: $identity['id'],
                    revision: $identity['revision'],
                    providedCapability: new CompanionActionCapability(
                        name: $action,
                        version: $identity['version'] ?? null,
                        digest: $digest,
                    ),
                    descriptor: $descriptor,
                    executable: $executable,
                    workingDirectory: $directory,
                    maxRuntimeSeconds: $this->maxRuntimeSeconds,
                    maxOutputBytes: $this->maxOutputBytes,
                    phpBinary: $this->phpBinary,
                    verifyPackage: function () use ($directory, $digest): void {
                        if (! hash_equals($digest, $this->directoryDigest($directory))) {
                            throw new RuntimeException('Ext companion package changed after discovery.');
                        }
                    },
                    executionPolicy: $this->executionPolicy === null
                        ? null
                        : fn (): CompanionExecutionPolicy => ($this->executionPolicy)($identity['id']),
                    warmedScope: $warmedScope,
                );
            }
        }

        return $providers;
    }

    /** @return array<string, mixed> */
    private function manifest(string $directory): array
    {
        $path = $directory.DIRECTORY_SEPARATOR.'plugin.json';
        if (! is_file($path) || is_link($path) || filesize($path) > 16777216) {
            throw new RuntimeException("Invalid ext package manifest [{$path}].");
        }
        $manifest = json_decode((string) file_get_contents($path), true, 128, JSON_THROW_ON_ERROR);
        if (! is_array($manifest) || ($manifest['apiVersion'] ?? null) !== self::PACKAGE_VERSION
            || ! is_array($manifest['descriptor'] ?? null)
            || ($manifest['descriptor']['apiVersion'] ?? null) !== self::PLUGIN_VERSION) {
            throw new RuntimeException("Invalid ext package manifest [{$path}].");
        }
        $identity = $manifest['descriptor']['identity'] ?? null;
        if (! is_array($identity)
            || ! $this->identifier($identity['id'] ?? null)
            || ! $this->revision($identity['revision'] ?? null)
            || (isset($identity['version']) && ! $this->revision($identity['version']))) {
            throw new RuntimeException("Invalid ext package identity [{$path}].");
        }

        return $manifest;
    }

    /**
     * A Planner-only package may use its top-level descriptor. A combined
     * release keeps the Spinner descriptor at the top level and places the
     * exact PHP process descriptor in namespaced metadata.
     *
     * @param array<string, mixed> $manifest
     * @return array<string, mixed>
     */
    private function companionDescriptor(array $manifest): array
    {
        $allMetadata = $manifest['metadata'] ?? [];
        if (! is_array($allMetadata)) {
            throw new RuntimeException('Invalid ext package metadata.');
        }
        $metadata = $allMetadata[self::COMPANION_METADATA] ?? null;
        if ($metadata === null) {
            return $manifest['descriptor'];
        }
        if (! is_array($metadata)
            || array_diff(array_keys($metadata), ['descriptor']) !== []
            || ! is_array($metadata['descriptor'] ?? null)) {
            throw new RuntimeException('Invalid ext companion metadata.');
        }
        $descriptor = $metadata['descriptor'];
        $identity = $descriptor['identity'] ?? null;
        $packageIdentity = $manifest['descriptor']['identity'];
        if (($descriptor['apiVersion'] ?? null) !== self::PLUGIN_VERSION
            || ! is_array($identity)
            || array_diff(array_keys($identity), ['id', 'revision', 'version']) !== []
            || ($identity['id'] ?? null) !== $packageIdentity['id']
            || ($identity['revision'] ?? null) !== $packageIdentity['revision']
            || ($identity['version'] ?? null) !== ($packageIdentity['version'] ?? null)
            || ! is_array($descriptor['contracts'] ?? null)) {
            throw new RuntimeException('Ext companion descriptor identity does not match its package.');
        }

        return $descriptor;
    }

    /** @param array<string, mixed> $descriptor
     * @return list<string>
     */
    private function companionActions(array $descriptor): array
    {
        if (! is_array($descriptor['contracts'] ?? null)) {
            throw new RuntimeException('Invalid ext companion descriptor.');
        }
        $actions = [];
        foreach ($descriptor['contracts'] as $contract) {
            if (! is_array($contract) || ($contract['name'] ?? null) !== 'gateway.companion'
                || ($contract['version'] ?? null) !== 'v1') {
                continue;
            }
            foreach ($contract['operations'] ?? [] as $operation) {
                $name = is_array($operation) ? ($operation['name'] ?? null) : null;
                if (! is_string($name) || preg_match('/\A[a-z0-9]+(?:[._-][a-z0-9]+)*\z/D', $name) !== 1
                    || isset($operation['surface']) || isset($operation['progress'])) {
                    throw new RuntimeException('Invalid ext companion action declaration.');
                }
                $actions[] = $name;
            }
        }
        if (count($actions) !== count(array_unique($actions))) {
            throw new RuntimeException('Duplicate ext companion action declaration.');
        }

        return $actions;
    }

    /**
     * @param array<string, mixed> $manifest
     * @return array{array<string, mixed>, array<string, array<string, mixed>>}
     */
    private function entrypoint(array $manifest): array
    {
        $artifacts = [];
        foreach ($manifest['artifacts'] ?? [] as $artifact) {
            if (! is_array($artifact) || ! $this->identifier($artifact['name'] ?? null)
                || ! is_string($artifact['path'] ?? null)
                || ! is_string($artifact['sha256'] ?? null)
                || preg_match('/\A[a-f0-9]{64}\z/D', $artifact['sha256']) !== 1
                || isset($artifacts[$artifact['name']])) {
                throw new RuntimeException('Invalid ext package artifact.');
            }
            $artifacts[$artifact['name']] = $artifact;
        }
        $selected = null;
        foreach ($manifest['entrypoints'] ?? [] as $entrypoint) {
            if (is_array($entrypoint) && ($entrypoint['name'] ?? null) === 'planner') {
                if ($selected !== null || ($entrypoint['runtime'] ?? null) !== 'php-jsonline'
                    || ! in_array(self::PLUGIN_VERSION, $entrypoint['protocols'] ?? [], true)
                    || ! isset($artifacts[$entrypoint['artifact'] ?? ''])) {
                    throw new RuntimeException('Invalid ext Planner companion entrypoint.');
                }
                $selected = $entrypoint;
            }
        }
        if ($selected === null) {
            throw new RuntimeException('Ext companion package has no planner entrypoint.');
        }

        return [$selected, $artifacts];
    }

    /** @param array<string, mixed> $manifest
     * @param array<string, array<string, mixed>> $artifacts
     */
    private function verifyPackage(string $directory, array $manifest, array $artifacts): void
    {
        $claimedFiles = ['plugin.json' => true];
        $claimedTrees = [];
        foreach ($artifacts as $artifact) {
            $tree = ($artifact['kind'] ?? '') === 'tree';
            if (($artifact['kind'] ?? '') !== '' && ! $tree) {
                throw new RuntimeException('Unsupported ext package artifact kind.');
            }
            $path = $this->securePath($directory, $artifact['path'], $tree);
            $actual = $tree ? $this->directoryDigest($path) : hash_file('sha256', $path);
            if (! is_string($actual) || ! hash_equals($artifact['sha256'], $actual)) {
                throw new RuntimeException("Ext package artifact [{$artifact['name']}] failed verification.");
            }
            if ($tree) {
                $claimedTrees[] = rtrim(str_replace('\\', '/', $artifact['path']), '/').'/';
            } else {
                $claimedFiles[str_replace('\\', '/', $artifact['path'])] = true;
            }
        }
        foreach ($this->files($directory) as $file) {
            if (isset($claimedFiles[$file])) {
                continue;
            }
            $owned = false;
            foreach ($claimedTrees as $tree) {
                if (str_starts_with($file, $tree)) {
                    $owned = true;
                    break;
                }
            }
            if (! $owned) {
                throw new RuntimeException("Ext package contains unlisted file [{$file}].");
            }
        }
    }

    private function securePath(string $directory, string $relative, bool $directoryExpected): string
    {
        if ($relative === '' || str_starts_with($relative, '/') || str_contains($relative, "\0")) {
            throw new RuntimeException('Invalid ext package artifact path.');
        }
        $parts = preg_split('~[\\\\/]~', $relative);
        if (! is_array($parts) || in_array('', $parts, true) || in_array('.', $parts, true) || in_array('..', $parts, true)) {
            throw new RuntimeException('Invalid ext package artifact path.');
        }
        $path = $directory;
        foreach ($parts as $part) {
            $path .= DIRECTORY_SEPARATOR.$part;
            if (is_link($path)) {
                throw new RuntimeException('Ext package artifacts cannot contain symbolic links.');
            }
        }
        if ($directoryExpected ? ! is_dir($path) : ! is_file($path)) {
            throw new RuntimeException('Ext package artifact is missing or has the wrong type.');
        }

        return $path;
    }

    /** @return list<string> */
    private function files(string $directory): array
    {
        $files = [];
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator(
            $directory,
            FilesystemIterator::SKIP_DOTS,
        ));
        foreach ($iterator as $file) {
            if ($file->isLink() || ! $file->isFile()) {
                throw new RuntimeException('Ext packages may contain only regular files and directories.');
            }
            $files[] = str_replace('\\', '/', substr($file->getPathname(), strlen($directory) + 1));
        }
        sort($files, SORT_STRING);

        return $files;
    }

    private function directoryDigest(string $directory): string
    {
        $context = hash_init('sha256');
        foreach ($this->files($directory) as $relative) {
            $digest = hash_file('sha256', $directory.DIRECTORY_SEPARATOR.str_replace('/', DIRECTORY_SEPARATOR, $relative));
            hash_update($context, './'.$relative.' '.$digest."\n");
        }

        return hash_final($context);
    }

    private function identifier(mixed $value): bool
    {
        return is_string($value) && preg_match('/\A[a-z0-9][a-z0-9._\/-]{0,255}\z/D', $value) === 1;
    }

    private function revision(mixed $value): bool
    {
        return is_string($value) && preg_match('/\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/D', $value) === 1;
    }
}
