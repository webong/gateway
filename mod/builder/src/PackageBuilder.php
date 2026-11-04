<?php

declare(strict_types=1);

namespace Webong\GatewayBuilder;

use FilesystemIterator;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use Throwable;

final class PackageBuilder
{
    /**
     * @return array<string, mixed>
     */
    public function build(
        string $manifestPath,
        string $version,
        string $output,
        ?string $revision = null,
        array $sourceRoots = [],
        array $values = [],
    ): array {
        $manifestPath = realpath($manifestPath) ?: throw new BuildException('Build manifest does not exist.');
        $sourceRoots = $this->sourceRoots(dirname($manifestPath), $sourceRoots);
        $values = $this->values($values);
        $build = json_decode((string) file_get_contents($manifestPath), true, flags: JSON_THROW_ON_ERROR);

        if (! is_array($build)) {
            throw new BuildException('Build manifest must contain a JSON object.');
        }

        $identity = $this->identifier($build['identity'] ?? null, 'identity');
        $version = $this->identifier($version, 'version');
        $expectedDirectory = str_replace('/', '%2f', $identity);

        if (basename(rtrim($output, DIRECTORY_SEPARATOR)) !== $expectedDirectory) {
            throw new BuildException("Output directory must be named {$expectedDirectory}.");
        }
        if (file_exists($output)) {
            throw new BuildException('Output directory already exists.');
        }

        $artifacts = $build['artifacts'] ?? null;
        if (! is_array($artifacts) || ! array_is_list($artifacts) || $artifacts === []) {
            throw new BuildException('Build manifest must declare at least one artifact.');
        }

        $builtArtifacts = [];
        $artifactNames = [];

        try {
            $this->makeDirectory($output);

            foreach ($artifacts as $artifact) {
                if (! is_array($artifact)) {
                    throw new BuildException('Each artifact must be a JSON object.');
                }

                $name = $this->identifier($artifact['name'] ?? null, 'artifact name');
                if (isset($artifactNames[$name])) {
                    throw new BuildException("Artifact [{$name}] is declared more than once.");
                }
                $artifactNames[$name] = true;

                $rootName = $artifact['sourceRoot'] ?? 'manifest';
                if (! is_string($rootName) || ! isset($sourceRoots[$rootName])) {
                    throw new BuildException("Unknown artifact source root [{$rootName}].");
                }
                $source = $this->sourcePath($sourceRoots[$rootName], $artifact['source'] ?? null);
                $path = $this->relativePath($artifact['path'] ?? null, 'artifact path');
                $target = $output.DIRECTORY_SEPARATOR.str_replace('/', DIRECTORY_SEPARATOR, $path);
                $kind = $artifact['kind'] ?? 'file';

                if ($kind === 'tree') {
                    if (! is_dir($source)) {
                        throw new BuildException("Tree artifact [{$name}] source is not a directory.");
                    }
                    $this->copyTree($source, $target);
                    $builtArtifacts[] = [
                        'name' => $name,
                        'path' => $path,
                        'kind' => 'tree',
                        'sha256' => $this->treeDigest($target),
                    ];
                    continue;
                }

                if ($kind !== 'file' || ! is_file($source) || is_link($source)) {
                    throw new BuildException("File artifact [{$name}] source is invalid.");
                }
                $this->makeDirectory(dirname($target));
                if (! copy($source, $target)) {
                    throw new BuildException("Cannot copy artifact [{$name}].");
                }
                chmod($target, ($artifact['executable'] ?? false) === true ? 0700 : 0600);
                $builtArtifacts[] = [
                    'name' => $name,
                    'path' => $path,
                    'sha256' => hash_file('sha256', $target),
                ];
            }

            $revision ??= hash('sha256', implode("\n", array_column($builtArtifacts, 'sha256')));
            $revision = $this->identifier($revision, 'revision');
            $packageIdentity = ['id' => $identity, 'revision' => $revision, 'version' => $version];

            $entrypoints = $build['entrypoints'] ?? [];
            if (! is_array($entrypoints) || ! array_is_list($entrypoints)) {
                throw new BuildException('Entrypoints must be a list.');
            }
            foreach ($entrypoints as $entrypoint) {
                if (! is_array($entrypoint) || ! isset($artifactNames[$entrypoint['artifact'] ?? ''])) {
                    throw new BuildException('Every entrypoint must reference a declared artifact.');
                }
            }

            $descriptor = $build['descriptor'] ?? [];
            $metadata = $build['metadata'] ?? [];
            if (! is_array($descriptor) || ! is_array($metadata)) {
                throw new BuildException('Descriptor and metadata must be JSON objects.');
            }

            $descriptor['apiVersion'] = 'ext.plugin/v1';
            $descriptor['identity'] = $packageIdentity;
            $result = [
                'apiVersion' => 'ext.package/v1',
                'descriptor' => $this->expand($descriptor, $packageIdentity, $values),
                'metadata' => $this->expand($metadata, $packageIdentity, $values),
                'artifacts' => $builtArtifacts,
                'entrypoints' => $entrypoints,
            ];
            $encoded = json_encode($result, JSON_PRETTY_PRINT | JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES)."\n";
            if (file_put_contents($output.'/plugin.json', $encoded, LOCK_EX) === false) {
                throw new BuildException('Cannot write plugin.json.');
            }

            return $result;
        } catch (Throwable $exception) {
            $this->removeTree($output);
            throw $exception;
        }
    }

    private function identifier(mixed $value, string $field): string
    {
        if (! is_string($value) || preg_match('/\A[A-Za-z0-9][A-Za-z0-9._:+\/-]{0,255}\z/D', $value) !== 1) {
            throw new BuildException("Invalid {$field}.");
        }

        return $value;
    }

    private function relativePath(mixed $value, string $field): string
    {
        if (! is_string($value) || $value === '' || str_starts_with($value, '/') || str_contains($value, '\\')) {
            throw new BuildException("Invalid {$field}.");
        }
        $segments = explode('/', $value);
        if (in_array('', $segments, true) || in_array('.', $segments, true) || in_array('..', $segments, true)) {
            throw new BuildException("Invalid {$field}.");
        }

        return $value;
    }

    private function sourcePath(string $root, mixed $source): string
    {
        $relative = $source === '.' ? '.' : $this->relativePath($source, 'artifact source');
        $candidate = $relative === '.' ? $root : $root.DIRECTORY_SEPARATOR.str_replace('/', DIRECTORY_SEPARATOR, $relative);
        if (is_link($candidate)) {
            throw new BuildException('Artifact source cannot be a symbolic link.');
        }
        $path = realpath($candidate);
        $root = realpath($root) ?: $root;
        if ($path === false || ($path !== $root && ! str_starts_with($path, $root.DIRECTORY_SEPARATOR))) {
            throw new BuildException('Artifact source escapes the build-manifest directory.');
        }

        return $path;
    }

    /**
     * @param array<string, string> $roots
     * @return array<string, string>
     */
    private function sourceRoots(string $manifestRoot, array $roots): array
    {
        $resolved = ['manifest' => realpath($manifestRoot) ?: $manifestRoot];
        foreach ($roots as $name => $root) {
            if (! is_string($name) || preg_match('/\A[A-Za-z][A-Za-z0-9._-]{0,63}\z/D', $name) !== 1 || $name === 'manifest') {
                throw new BuildException('Invalid named source root.');
            }
            if (! is_string($root) || is_link($root) || ($path = realpath($root)) === false || ! is_dir($path)) {
                throw new BuildException("Named source root [{$name}] is invalid.");
            }
            $resolved[$name] = $path;
        }

        return $resolved;
    }

    /**
     * @param array<string, string> $values
     * @return array<string, string>
     */
    private function values(array $values): array
    {
        foreach ($values as $name => $value) {
            if (! is_string($name) || preg_match('/\A[A-Za-z][A-Za-z0-9._-]{0,63}\z/D', $name) !== 1 || ! is_string($value)) {
                throw new BuildException('Invalid build value.');
            }
            if (in_array($name, ['identity', 'revision', 'version'], true)) {
                throw new BuildException("Build value [{$name}] is reserved.");
            }
        }

        return $values;
    }

    private function makeDirectory(string $directory): void
    {
        if (! is_dir($directory) && ! mkdir($directory, 0700, true) && ! is_dir($directory)) {
            throw new BuildException("Cannot create directory [{$directory}].");
        }
    }

    private function copyTree(string $source, string $destination): void
    {
        $this->makeDirectory($destination);
        $iterator = new RecursiveIteratorIterator(
            new RecursiveDirectoryIterator($source, FilesystemIterator::SKIP_DOTS),
            RecursiveIteratorIterator::SELF_FIRST,
        );
        foreach ($iterator as $entry) {
            if ($entry->isLink()) {
                throw new BuildException('Artifact trees cannot contain symbolic links.');
            }
            $relative = substr($entry->getPathname(), strlen($source) + 1);
            $target = $destination.DIRECTORY_SEPARATOR.$relative;
            if ($entry->isDir()) {
                $this->makeDirectory($target);
            } elseif ($entry->isFile()) {
                if (! copy($entry->getPathname(), $target)) {
                    throw new BuildException("Cannot copy tree entry [{$relative}].");
                }
                chmod($target, 0600);
            } else {
                throw new BuildException('Artifact trees may contain only regular files and directories.');
            }
        }
    }

    private function treeDigest(string $directory): string
    {
        $files = [];
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $file) {
            if ($file->isFile()) {
                $files[] = str_replace('\\', '/', substr($file->getPathname(), strlen($directory) + 1));
            }
        }
        sort($files, SORT_STRING);
        $context = hash_init('sha256');
        foreach ($files as $relative) {
            hash_update($context, './'.$relative.' '.hash_file('sha256', $directory.'/'.$relative)."\n");
        }

        return hash_final($context);
    }

    /** @param array{id: string, revision: string, version: string} $identity */
    private function expand(mixed $value, array $identity, array $values): mixed
    {
        if (is_string($value)) {
            $replacements = [
                '{{identity}}' => $identity['id'],
                '{{revision}}' => $identity['revision'],
                '{{version}}' => $identity['version'],
            ];
            foreach ($values as $name => $replacement) {
                $replacements['{{'.$name.'}}'] = $replacement;
            }

            return strtr($value, $replacements);
        }
        if (! is_array($value)) {
            return $value;
        }
        foreach ($value as $key => $child) {
            $value[$key] = $this->expand($child, $identity, $values);
        }
        if (($value['apiVersion'] ?? null) === 'ext.plugin/v1') {
            $value['identity'] = $identity;
        }

        return $value;
    }

    private function removeTree(string $directory): void
    {
        if (! is_dir($directory)) {
            return;
        }
        $iterator = new RecursiveIteratorIterator(
            new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS),
            RecursiveIteratorIterator::CHILD_FIRST,
        );
        foreach ($iterator as $entry) {
            $entry->isDir() ? @rmdir($entry->getPathname()) : @unlink($entry->getPathname());
        }
        @rmdir($directory);
    }
}
