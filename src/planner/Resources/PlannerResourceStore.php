<?php

declare(strict_types=1);

namespace Webong\Gateway\Resources;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use InvalidArgumentException;
use stdClass;

/** One encrypted, versioned document store; kind-specific modules own validation. */
final class PlannerResourceStore
{
    /** @return list<stdClass> */
    public function all(string $kind): array
    {
        return array_map($this->decode(...), DB::table('configurations')
            ->where('kind', $kind)->orderBy('key')->get()->all());
    }

    public function find(string $kind, string $id): ?stdClass
    {
        if (! Str::isUuid($id)) {
            return null;
        }
        $row = DB::table('configurations')->where('kind', $kind)->where('id', $id)->first();

        return $row === null ? null : $this->decode($row);
    }

    public function byKey(string $kind, string $key): ?stdClass
    {
        $row = DB::table('configurations')->where('kind', $kind)
            ->where('key_hash', $this->keyHash($kind, $key))->first();

        return $row === null ? null : $this->decode($row);
    }

    /** @param array<string, mixed> $document */
    public function create(string $kind, string $key, array $document, bool $enabled = true): stdClass
    {
        return DB::transaction(function () use ($kind, $key, $document, $enabled): stdClass {
            $id = (string) Str::uuid();
            $now = now('UTC');
            $encoded = $this->encode($document);
            $hash = $this->keyHash($kind, $key);
            DB::table('configurations')->insert([
                'id' => $id, 'kind' => $kind, 'key_hash' => $hash,
                'key' => $key, 'document' => $encoded, 'schema_version' => 1,
                'enabled' => $enabled,
                'revision' => 1, 'created_at' => $now, 'updated_at' => $now,
            ]);
            $this->record($id, $kind, $hash, 1, 'created', $encoded, $enabled, $now);

            return $this->find($kind, $id);
        });
    }

    /** @param array<string, mixed> $document */
    public function replace(string $kind, string $id, string $key, array $document, bool $enabled, int $expectedRevision): ?stdClass
    {
        if (! Str::isUuid($id)) {
            return null;
        }
        return DB::transaction(function () use ($kind, $id, $key, $document, $enabled, $expectedRevision): ?stdClass {
            $current = $this->find($kind, $id);
            if ($current === null) {
                return null;
            }
            if ($current->revision !== $expectedRevision) {
                throw new StaleResource('Planner resource revision has changed.');
            }
            $encoded = $this->encode($document);
            $hash = $this->keyHash($kind, $key);
            $now = now('UTC');
            $updated = DB::table('configurations')->where('kind', $kind)->where('id', $id)
                ->where('revision', $expectedRevision)->increment('revision', 1, [
                    'key_hash' => $hash, 'key' => $key,
                    'document' => $encoded, 'enabled' => $enabled, 'updated_at' => $now,
                ]);
            if ($updated !== 1) {
                throw new StaleResource('Planner resource revision has changed.');
            }
            $this->record($id, $kind, $hash, $expectedRevision + 1, 'replaced', $encoded, $enabled, $now);

            return $this->find($kind, $id);
        });
    }

    public function delete(string $kind, string $id, int $expectedRevision): bool
    {
        if (! Str::isUuid($id)) {
            return false;
        }
        return DB::transaction(function () use ($kind, $id, $expectedRevision): bool {
            $current = $this->find($kind, $id);
            if ($current === null) {
                return false;
            }
            if ($current->revision !== $expectedRevision) {
                throw new StaleResource('Planner resource revision has changed.');
            }
            $deleted = DB::table('configurations')->where('kind', $kind)->where('id', $id)
                ->where('revision', $expectedRevision)->delete();
            if ($deleted !== 1) {
                throw new StaleResource('Planner resource revision has changed.');
            }
            $this->record($id, $kind, $this->keyHash($kind, $current->key), $expectedRevision + 1,
                'deleted', $this->encode($current->document), $current->enabled, now('UTC'));

            return true;
        });
    }

    /** @return list<stdClass> */
    public function history(string $kind, string $id): array
    {
        if (! Str::isUuid($id)) {
            return [];
        }

        return DB::table('configuration_revisions')->where('kind', $kind)->where('resource_id', $id)
            ->orderBy('revision')->get(['revision', 'operation', 'created_at'])->all();
    }

    private function record(string $id, string $kind, string $hash, int $revision, string $operation,
        string $document, bool $enabled, mixed $at): void
    {
        DB::table('configuration_revisions')->insert([
            'resource_id' => $id, 'kind' => $kind, 'key_hash' => $hash, 'revision' => $revision,
            'operation' => $operation, 'document' => $document, 'enabled' => $enabled,
            'created_at' => $at,
        ]);
    }

    private function keyHash(string $kind, string $key): string
    {
        if (preg_match('/^[a-z][a-z0-9.-]{0,95}$/D', $kind) !== 1 ||
            $key === '' || strlen($key) > 4096) {
            throw new InvalidArgumentException('Invalid Planner resource identity.');
        }

        return hash('sha256', $kind."\n".$key);
    }

    /** @param array<string, mixed> $document */
    private function encode(array $document): string
    {
        return Crypt::encryptString(json_encode($document, JSON_THROW_ON_ERROR));
    }

    private function decode(stdClass $row): stdClass
    {
        $row->document = json_decode(Crypt::decryptString($row->document), true, 512, JSON_THROW_ON_ERROR);
        $row->enabled = (bool) $row->enabled;
        $row->schema_version = (int) $row->schema_version;
        $row->revision = (int) $row->revision;

        return $row;
    }
}
