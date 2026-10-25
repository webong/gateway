<?php

declare(strict_types=1);

namespace Webong\Gateway\Automation;

use FilesystemIterator;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use RuntimeException;
use Webong\Gateway\Companion\ExtensionArtifactReconciler;

/** Discovers automation capabilities from verified installed ext packages. */
final readonly class ExtAutomationPackageCatalog
{
    private const string METADATA = 'gateway.automation/v1';

    public function __construct(
        private string $root,
        private ExtensionArtifactReconciler $artifacts,
    ) {}

    /** @return list<array{id: string, target: string, capabilities: list<string>}> */
    public function languages(): array
    {
        $languages = [];
        foreach ($this->extensions() as $definitions) {
            foreach ($definitions as $language) {
                if (isset($languages[$language['id']])) {
                    throw new RuntimeException("Automation language [{$language['id']}] is provided by multiple extensions.");
                }
                $languages[$language['id']] = $language;
            }
        }
        ksort($languages, SORT_STRING);

        return array_values($languages);
    }

    /** @return array<string, list<array{id: string, target: string, capabilities: list<string>}>> */
    public function extensions(): array
    {
        $desired = array_fill_keys($this->artifacts->reconcile(), true);
        if ($desired === [] || trim($this->root) === '' || ! is_dir($this->root)) {
            return [];
        }
        $extensions = [];
        $owners = [];
        foreach (array_keys($desired) as $extension) {
            $directory = $this->root.DIRECTORY_SEPARATOR.str_replace('/', '%2f', $extension);
            $manifest = $this->manifest($directory, $extension);
            $this->verifyArtifacts($directory, $manifest);
            $metadata = $manifest['metadata'][self::METADATA] ?? null;
            if ($metadata === null) {
                continue;
            }
            if (! is_array($metadata) || array_diff(array_keys($metadata), ['languages']) !== []
                || ! is_array($metadata['languages'] ?? null)) {
                throw new RuntimeException("Invalid automation metadata for Gateway extension [{$extension}].");
            }
            foreach ($metadata['languages'] as $language) {
                if (! is_array($language) || array_diff(array_keys($language), ['id', 'target', 'capabilities']) !== []
                    || ! is_string($language['id'] ?? null)
                    || preg_match('/\A[a-z][a-z0-9_-]*\z/D', $language['id']) !== 1
                    || ($language['target'] ?? null) !== 'spinner'
                    || ! is_array($language['capabilities'] ?? null) || $language['capabilities'] === []
                    || array_diff($language['capabilities'], ['route', 'schedule', 'credential']) !== []
                    || count($language['capabilities']) !== count(array_unique($language['capabilities']))) {
                    throw new RuntimeException("Invalid automation language metadata for Gateway extension [{$extension}].");
                }
                if (! $this->declaresLanguage($manifest['descriptor']['contracts'], $language['id'])) {
                    throw new RuntimeException("Gateway extension [{$extension}] metadata is not declared by its descriptor.");
                }
                if (isset($owners[$language['id']])) {
                    throw new RuntimeException("Automation language [{$language['id']}] is provided by multiple extensions.");
                }
                $owners[$language['id']] = $extension;
                $extensions[$extension][] = [
                    'id' => $language['id'],
                    'target' => 'spinner',
                    'capabilities' => array_values($language['capabilities']),
                ];
            }
        }
        ksort($extensions, SORT_STRING);
        foreach ($extensions as &$languages) {
            usort($languages, static fn (array $left, array $right): int => $left['id'] <=> $right['id']);
        }
        unset($languages);

        return $extensions;
    }

    /** @return array<string, mixed> */
    private function manifest(string $directory, string $extension): array
    {
        $path = $directory.DIRECTORY_SEPARATOR.'plugin.json';
        if (! is_file($path) || is_link($path) || filesize($path) > 16777216) {
            throw new RuntimeException("Invalid installed Gateway extension [{$extension}].");
        }
        $manifest = json_decode((string) file_get_contents($path), true, 128, JSON_THROW_ON_ERROR);
        $identity = is_array($manifest) ? ($manifest['descriptor']['identity'] ?? null) : null;
        if (! is_array($manifest) || ($manifest['apiVersion'] ?? null) !== 'ext.package/v1'
            || ($manifest['descriptor']['apiVersion'] ?? null) !== 'ext.plugin/v1'
            || ! is_array($identity) || ($identity['id'] ?? null) !== $extension
            || ! is_array($manifest['descriptor']['contracts'] ?? null)) {
            throw new RuntimeException("Invalid installed Gateway extension [{$extension}].");
        }

        return $manifest;
    }

    /** @param list<mixed> $contracts */
    private function declaresLanguage(array $contracts, string $language): bool
    {
        foreach ($contracts as $contract) {
            if (! is_array($contract) || ($contract['name'] ?? null) !== 'gateway.automation'
                || ($contract['version'] ?? null) !== 'v1') {
                continue;
            }
            foreach ($contract['operations'] ?? [] as $operation) {
                if (is_array($operation) && ($operation['name'] ?? null) === 'execute'
                    && ($operation['surface'] ?? null) === $language) {
                    return true;
                }
            }
        }

        return false;
    }

    /** @param array<string, mixed> $manifest */
    private function verifyArtifacts(string $directory, array $manifest): void
    {
        foreach ($manifest['artifacts'] ?? [] as $artifact) {
            if (! is_array($artifact) || ! is_string($artifact['path'] ?? null)
                || ! is_string($artifact['sha256'] ?? null)
                || preg_match('/\A[a-f0-9]{64}\z/D', $artifact['sha256']) !== 1) {
                throw new RuntimeException('Invalid Gateway extension artifact declaration.');
            }
            $path = $this->securePath($directory, $artifact['path']);
            $actual = ($artifact['kind'] ?? null) === 'tree'
                ? $this->directoryDigest($path)
                : hash_file('sha256', $path);
            if (! is_string($actual) || ! hash_equals($artifact['sha256'], $actual)) {
                throw new RuntimeException('Gateway extension artifact failed integrity verification.');
            }
        }
    }

    private function securePath(string $directory, string $relative): string
    {
        if ($relative === '' || str_starts_with($relative, '/') || str_contains($relative, "\0")) {
            throw new RuntimeException('Invalid Gateway extension artifact path.');
        }
        $path = $directory;
        foreach (preg_split('~[\\\\/]~', $relative) ?: [] as $part) {
            if ($part === '' || $part === '.' || $part === '..') {
                throw new RuntimeException('Invalid Gateway extension artifact path.');
            }
            $path .= DIRECTORY_SEPARATOR.$part;
            if (is_link($path)) {
                throw new RuntimeException('Gateway extension artifacts cannot contain symbolic links.');
            }
        }
        if (! is_file($path) && ! is_dir($path)) {
            throw new RuntimeException('Gateway extension artifact is missing.');
        }

        return $path;
    }

    private function directoryDigest(string $directory): string
    {
        $files = [];
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $file) {
            if ($file->isLink() || ! $file->isFile()) {
                throw new RuntimeException('Gateway extension artifact tree is invalid.');
            }
            $files[] = str_replace('\\', '/', substr($file->getPathname(), strlen($directory) + 1));
        }
        sort($files, SORT_STRING);
        $context = hash_init('sha256');
        foreach ($files as $relative) {
            $digest = hash_file('sha256', $directory.DIRECTORY_SEPARATOR.str_replace('/', DIRECTORY_SEPARATOR, $relative));
            hash_update($context, './'.$relative.' '.$digest."\n");
        }

        return hash_final($context);
    }
}
