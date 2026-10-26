<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use Webong\Gateway\Resources\StaleResource;

/** Encrypted, non-revisioned secret documents owned by one Extension identity. */
final readonly class ExtensionSecretStore
{
    private const int MAX_VALUE_BYTES = 16384;

    private const int MAX_DOCUMENT_BYTES = 65536;

    public function __construct(private CompanionActionActivationStore $extensions) {}

    /** @return list<array{reference: string, version: int}>|null */
    public function all(string $extension): ?array
    {
        if ($this->extensions->find($extension) === null) {
            return null;
        }
        $document = $this->document($extension);
        $result = [];
        foreach ($document as $reference => $entry) {
            $result[] = ['reference' => $reference, 'version' => (int) $entry['version']];
        }
        usort($result, static fn (array $left, array $right): int => $left['reference'] <=> $right['reference']);

        return $result;
    }

    /** @return array{reference: string, version: int}|null */
    public function metadata(string $extension, string $reference): ?array
    {
        $this->validateReference($reference);
        if ($this->extensions->find($extension) === null) {
            return null;
        }
        $entry = $this->document($extension)[$reference] ?? null;

        return is_array($entry)
            ? ['reference' => $reference, 'version' => (int) $entry['version']]
            : null;
    }

    /** @param array<string, mixed> $value
     *  @return array{reference: string, version: int}|null
     */
    public function put(string $extension, string $reference, array $value, ?int $expectedVersion): ?array
    {
        $this->validateReference($reference);
        $this->validateValue($value);

        return DB::transaction(function () use ($extension, $reference, $value, $expectedVersion): ?array {
            if ($this->extensions->find($extension) === null) {
                return null;
            }
            $row = DB::table('configuration_secrets')->where('resource_id', $extension)->lockForUpdate()->first();
            $document = $row === null ? [] : $this->decode((string) $row->ciphertext);
            $current = $document[$reference] ?? null;
            if ($current === null && $expectedVersion !== null) {
                throw new StaleResource('Gateway Extension secret does not exist.');
            }
            if ($current !== null && ($expectedVersion === null || (int) $current['version'] !== $expectedVersion)) {
                throw new StaleResource('Gateway Extension secret has changed.');
            }
            $version = $current === null ? 1 : ((int) $current['version']) + 1;
            $document[$reference] = ['version' => $version, 'value' => $value];
            ksort($document);
            $encoded = json_encode($document, JSON_THROW_ON_ERROR);
            if (strlen($encoded) > self::MAX_DOCUMENT_BYTES) {
                throw new InvalidArgumentException('Gateway Extension secrets exceed 64 KiB.');
            }
            $ciphertext = Crypt::encryptString($encoded);
            $now = now('UTC');
            if ($row === null) {
                DB::table('configuration_secrets')->insert([
                    'resource_id' => $extension, 'ciphertext' => $ciphertext, 'version' => 1,
                    'created_at' => $now, 'updated_at' => $now,
                ]);
            } else {
                DB::table('configuration_secrets')->where('resource_id', $extension)->update([
                    'ciphertext' => $ciphertext, 'version' => ((int) $row->version) + 1, 'updated_at' => $now,
                ]);
            }

            return ['reference' => $reference, 'version' => $version];
        });
    }

    public function delete(string $extension, string $reference, int $expectedVersion): bool
    {
        $this->validateReference($reference);

        return DB::transaction(function () use ($extension, $reference, $expectedVersion): bool {
            if ($this->extensions->find($extension) === null) {
                return false;
            }
            $row = DB::table('configuration_secrets')->where('resource_id', $extension)->lockForUpdate()->first();
            if ($row === null) {
                return false;
            }
            $document = $this->decode((string) $row->ciphertext);
            $current = $document[$reference] ?? null;
            if ($current === null) {
                return false;
            }
            if ((int) $current['version'] !== $expectedVersion) {
                throw new StaleResource('Gateway Extension secret has changed.');
            }
            unset($document[$reference]);
            if ($document === []) {
                DB::table('configuration_secrets')->where('resource_id', $extension)->delete();
            } else {
                $now = now('UTC');
                DB::table('configuration_secrets')->where('resource_id', $extension)->update([
                    'ciphertext' => Crypt::encryptString(json_encode($document, JSON_THROW_ON_ERROR)),
                    'version' => ((int) $row->version) + 1, 'updated_at' => $now,
                ]);
            }

            return true;
        });
    }

    /** @return array{reference: string, version: int, value: array<string, mixed>}|null */
    public function resolve(string $identity, string $reference): ?array
    {
        $this->validateReference($reference);
        $extension = $this->extensions->byExtension($identity);
        if ($extension === null || ! $extension->enabled) {
            return null;
        }
        $entry = $this->document($extension->id)[$reference] ?? null;
        if (! is_array($entry) || ! is_array($entry['value'] ?? null)) {
            return null;
        }

        return ['reference' => $reference, 'version' => (int) $entry['version'], 'value' => $entry['value']];
    }

    /** @return array<string, array{version: int, value: array<string, mixed>}> */
    private function document(string $extension): array
    {
        $ciphertext = DB::table('configuration_secrets')->where('resource_id', $extension)->value('ciphertext');

        return is_string($ciphertext) ? $this->decode($ciphertext) : [];
    }

    /** @return array<string, array{version: int, value: array<string, mixed>}> */
    private function decode(string $ciphertext): array
    {
        $document = json_decode(Crypt::decryptString($ciphertext), true, 32, JSON_THROW_ON_ERROR);
        if (! is_array($document)) {
            throw new InvalidArgumentException('Gateway Extension secret document is invalid.');
        }

        return $document;
    }

    private function validateReference(string $reference): void
    {
        if (preg_match('/\A[a-z][a-z0-9._-]{0,63}\z/D', $reference) !== 1) {
            throw new InvalidArgumentException('Invalid Gateway Extension secret reference.');
        }
    }

    /** @param array<string, mixed> $value */
    private function validateValue(array $value): void
    {
        if ($value === [] || array_is_list($value)) {
            throw new InvalidArgumentException('Gateway Extension secret value must be an object.');
        }
        $encoded = json_encode($value, JSON_THROW_ON_ERROR);
        if (strlen($encoded) > self::MAX_VALUE_BYTES) {
            throw new InvalidArgumentException('Gateway Extension secret value exceeds 16 KiB.');
        }
    }
}
