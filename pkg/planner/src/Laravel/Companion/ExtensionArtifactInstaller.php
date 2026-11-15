<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Companion;

use FilesystemIterator;
use Illuminate\Http\Client\Factory as HttpFactory;
use PharData;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use RuntimeException;
use Throwable;

/** Materializes a pinned ext.package/v1 archive into Planner's local cache. */
final readonly class ExtensionArtifactInstaller
{
    private const int MAX_ARCHIVE_BYTES = 536870912;

    public function __construct(private string $root, private HttpFactory $http) {}

    public function install(object $row): void
    {
        $package = $row->document['package'] ?? null;
        $artifact = is_array($package) ? ($package['artifact'] ?? null) : null;
        if (! is_array($artifact)) {
            return;
        }
        if (trim($this->root) === '') {
            throw new RuntimeException('Planner extension artifact storage is not configured.');
        }
        $receipt = $this->receipt($row->key);
        $target = $this->root.DIRECTORY_SEPARATOR.str_replace('/', '%2f', $row->key);
        if (is_file($receipt) && is_dir($target)) {
            $saved = json_decode((string) file_get_contents($receipt), true);
            if (is_array($saved) && ($saved['revision'] ?? null) === $row->revision
                && ($saved['sha256'] ?? null) === $artifact['sha256']) {
                return;
            }
        }

        if (! is_dir($this->root) && ! mkdir($this->root, 0700, true) && ! is_dir($this->root)) {
            throw new RuntimeException('Unable to create Planner extension artifact storage.');
        }
        $temporary = $this->root.DIRECTORY_SEPARATOR.'.artifact-'.bin2hex(random_bytes(8));
        if (! mkdir($temporary, 0700)) {
            throw new RuntimeException('Unable to stage the Gateway extension artifact.');
        }
        try {
            $compressed = $temporary.DIRECTORY_SEPARATOR.'package.tar.gz';
            $archivePath = $temporary.DIRECTORY_SEPARATOR.'package.tar';
            $this->downloadArchive($artifact, $compressed);
            $this->expandArchive($compressed, $archivePath);
            $extracted = $temporary.DIRECTORY_SEPARATOR.'extracted';
            mkdir($extracted, 0700);
            $phar = new PharData($archivePath);
            $prefix = 'phar://'.$archivePath.'/';
            $files = 0;
            $bytes = 0;
            foreach (new RecursiveIteratorIterator($phar) as $entry) {
                $name = str_replace('\\', '/', substr($entry->getPathname(), strlen($prefix)));
                if ($name === '' || str_starts_with($name, '/') || str_contains('/'.$name.'/', '/../')
                    || $entry->isLink() || (! $entry->isFile() && ! $entry->isDir())) {
                    throw new RuntimeException('Gateway extension artifact contains an unsafe archive entry.');
                }
                if ($entry->isFile()) {
                    $files++;
                    $bytes += $entry->getSize();
                    if ($files > 250000 || $bytes > self::MAX_ARCHIVE_BYTES) {
                        throw new RuntimeException('Gateway extension artifact exceeds extraction limits.');
                    }
                }
            }
            if (! $phar->extractTo($extracted, null, false)) {
                throw new RuntimeException('Unable to extract the Gateway extension artifact.');
            }
            unset($phar);
            $source = $this->packageRoot($extracted);
            $manifest = json_decode((string) file_get_contents($source.DIRECTORY_SEPARATOR.'plugin.json'), true);
            $identity = is_array($manifest) ? ($manifest['descriptor']['identity'] ?? null) : null;
            if (! is_array($identity) || ($identity['id'] ?? null) !== $row->key
                || ($identity['version'] ?? null) !== ($package['version'] ?? null)) {
                throw new RuntimeException('Gateway extension artifact identity does not match desired state.');
            }

            $backup = null;
            if (file_exists($target)) {
                $backup = $this->root.DIRECTORY_SEPARATOR.'.backup-'.bin2hex(random_bytes(8));
                if (! rename($target, $backup)) {
                    throw new RuntimeException('Unable to replace the installed Gateway extension.');
                }
            }
            try {
                if (! rename($source, $target)) {
                    throw new RuntimeException('Unable to install the Gateway extension artifact.');
                }
                $this->writeReceipt($row->key, ['revision' => $row->revision, 'sha256' => $artifact['sha256']]);
                if ($backup !== null) {
                    $this->removeDirectory($backup);
                }
            } catch (Throwable $exception) {
                if (is_dir($target)) {
                    $this->removeDirectory($target);
                }
                if ($backup !== null && is_dir($backup)) {
                    @rename($backup, $target);
                }
                throw $exception;
            }
        } finally {
            if (is_dir($temporary)) {
                $this->removeDirectory($temporary);
            }
        }
    }

    /** @param array{url: string, sha256: string} $artifact */
    private function downloadArchive(array $artifact, string $destination): void
    {
        // Keep both transport buffers and archive expansion independent of the
        // runtime's size. Public release origins may redirect to an HTTPS CDN.
        $deadline = hrtime(true) + 60_000_000_000;
        $response = $this->http->timeout(60)->withOptions([
            'stream' => true,
            'read_timeout' => 10,
            'allow_redirects' => ['max' => 5, 'protocols' => ['https'], 'referer' => false],
        ])->get($artifact['url']);
        $input = $response->toPsrResponse()->getBody();
        $output = null;
        try {
            if (! $response->successful()) {
                throw new RuntimeException('Unable to download the desired Gateway extension artifact.');
            }
            $output = fopen($destination, 'wb');
            if ($output === false) {
                throw new RuntimeException('Unable to stage the Gateway extension artifact.');
            }
            $hash = hash_init('sha256');
            $bytes = 0;
            while (! $input->eof()) {
                if (hrtime(true) >= $deadline) {
                    throw new RuntimeException('Gateway extension artifact download timed out.');
                }
                $chunk = $input->read(65536);
                if (hrtime(true) >= $deadline) {
                    throw new RuntimeException('Gateway extension artifact download timed out.');
                }
                $bytes += strlen($chunk);
                if ($bytes > self::MAX_ARCHIVE_BYTES || ($chunk === '' && ! $input->eof())) {
                    throw new RuntimeException('Gateway extension artifact failed transport integrity verification.');
                }
                hash_update($hash, $chunk);
                if (fwrite($output, $chunk) !== strlen($chunk)) {
                    throw new RuntimeException('Unable to stage the Gateway extension artifact.');
                }
            }
            if ($bytes === 0 || ! hash_equals($artifact['sha256'], hash_final($hash))) {
                throw new RuntimeException('Gateway extension artifact failed transport integrity verification.');
            }
        } finally {
            $input->close();
            if (is_resource($output)) {
                fclose($output);
            }
        }
    }

    private function expandArchive(string $compressed, string $destination): void
    {
        if (file_get_contents($compressed, false, null, 0, 2) !== "\x1f\x8b") {
            throw new RuntimeException('Gateway extension artifact is not a gzip archive.');
        }
        $input = gzopen($compressed, 'rb');
        $output = fopen($destination, 'wb');
        try {
            if ($input === false || $output === false) {
                throw new RuntimeException('Unable to expand the Gateway extension artifact.');
            }
            $bytes = 0;
            while (! gzeof($input)) {
                $chunk = gzread($input, 65536);
                if ($chunk === false || ($chunk === '' && ! gzeof($input))) {
                    throw new RuntimeException('Unable to expand the Gateway extension artifact.');
                }
                $bytes += strlen($chunk);
                if ($bytes > self::MAX_ARCHIVE_BYTES) {
                    throw new RuntimeException('Gateway extension artifact exceeds extraction limits.');
                }
                if (fwrite($output, $chunk) !== strlen($chunk)) {
                    throw new RuntimeException('Unable to expand the Gateway extension artifact.');
                }
            }
        } finally {
            if (is_resource($input)) {
                gzclose($input);
            }
            if (is_resource($output)) {
                fclose($output);
            }
        }
    }

    private function packageRoot(string $extracted): string
    {
        if (is_file($extracted.DIRECTORY_SEPARATOR.'plugin.json')) {
            return $extracted;
        }
        $entries = array_values(array_filter(scandir($extracted) ?: [],
            static fn (string $entry): bool => $entry !== '.' && $entry !== '..'));
        if (count($entries) !== 1 || ! is_file($extracted.DIRECTORY_SEPARATOR.$entries[0].DIRECTORY_SEPARATOR.'plugin.json')) {
            throw new RuntimeException('Gateway extension artifact must contain one package root.');
        }

        return $extracted.DIRECTORY_SEPARATOR.$entries[0];
    }

    /** @param array{revision: int, sha256: string} $receipt */
    private function writeReceipt(string $extension, array $receipt): void
    {
        $path = $this->receipt($extension);
        if (! is_dir(dirname($path)) && ! mkdir(dirname($path), 0700, true) && ! is_dir(dirname($path))) {
            throw new RuntimeException('Unable to create Gateway extension receipt storage.');
        }
        $temporary = $path.'.'.bin2hex(random_bytes(6));
        file_put_contents($temporary, json_encode($receipt, JSON_THROW_ON_ERROR), LOCK_EX);
        chmod($temporary, 0600);
        if (! rename($temporary, $path)) {
            @unlink($temporary);
            throw new RuntimeException('Unable to save the Gateway extension receipt.');
        }
    }

    private function receipt(string $extension): string
    {
        return $this->root.DIRECTORY_SEPARATOR.'.desired'.DIRECTORY_SEPARATOR.hash('sha256', $extension).'.json';
    }

    private function removeDirectory(string $directory): void
    {
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
}
