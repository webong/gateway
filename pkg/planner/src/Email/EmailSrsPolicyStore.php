<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Encrypted, versioned SRS policy; old keys remain available for in-flight returns. */
final readonly class EmailSrsPolicyStore
{
    private const KIND = 'email.srs-policy';
    private const KEY = 'default';

    public function __construct(private PlannerResourceStore $resources) {}

    public function find(): ?stdClass
    {
        return $this->resources->byKey(self::KIND, self::KEY);
    }

    public function create(array $input): stdClass
    {
        return $this->resources->create(self::KIND, self::KEY, $this->document($input), $input['enabled'] ?? true);
    }

    public function replace(array $input, int $revision): ?stdClass
    {
        $current = $this->find();
        if ($current === null) {
            return null;
        }
        $document = $this->document($input, $current->document);
        if ($this->hasPendingReturns() && ($document['domain'] !== $current->document['domain'] ||
            array_diff($current->document['keys'], $document['keys']) !== [])) {
            throw new InvalidArgumentException('SRS return domain or verification keys cannot change while returns are pending.');
        }

        return $this->resources->replace(self::KIND, $current->id, self::KEY, $document,
            $input['enabled'] ?? $current->enabled, $revision);
    }

    public function delete(int $revision): bool
    {
        $current = $this->find();
        if ($current === null) {
            return false;
        }
        if ($this->hasPendingReturns()) {
            throw new InvalidArgumentException('SRS policy cannot be deleted while returns are pending.');
        }

        return $this->resources->delete(self::KIND, $current->id, $revision);
    }

    public function codec(): ?SrsCodec
    {
        $row = $this->find();
        if ($row === null) {
            return null;
        }

        return new SrsCodec($row->document['domain'], array_map(
            static fn (string $key): string => base64_decode($key, true), $row->document['keys']),
            $row->document['max_age_seconds']);
    }

    public function rewriteEnabled(): bool
    {
        $row = $this->find();

        return $row !== null && $row->enabled && $row->document['rewrite_enabled'];
    }

    private function hasPendingReturns(): bool
    {
        return DB::table('srs_returns')->where('expires_at', '>', now())->exists();
    }

    private function document(array $input, ?array $previous = null): array
    {
        if (array_diff(array_keys($input), ['domain', 'keys', 'max_age_seconds', 'rewrite_enabled', 'enabled']) !== [] ||
            (array_key_exists('enabled', $input) && ! is_bool($input['enabled'])) ||
            (array_key_exists('rewrite_enabled', $input) && ! is_bool($input['rewrite_enabled']))) {
            throw new InvalidArgumentException('Invalid SRS policy.');
        }
        $domain = $input['domain'] ?? $previous['domain'] ?? null;
        $keys = $input['keys'] ?? $previous['keys'] ?? null;
        $maxAge = $input['max_age_seconds'] ?? $previous['max_age_seconds'] ?? 21 * 86400;
        if (! is_string($domain) || ! is_array($keys) || ! array_is_list($keys) || count($keys) < 1 || count($keys) > 8 ||
            ! is_int($maxAge)) {
            throw new InvalidArgumentException('Invalid SRS policy.');
        }
        $decoded = [];
        foreach ($keys as $key) {
            $bytes = is_string($key) ? base64_decode($key, true) : false;
            if ($bytes === false || strlen($bytes) < 32 || strlen($bytes) > 128) {
                throw new InvalidArgumentException('SRS keys must be base64-encoded secrets of 32 to 128 bytes.');
            }
            $decoded[] = $bytes;
        }
        new SrsCodec($domain, $decoded, $maxAge);

        return ['domain' => $domain, 'keys' => $keys, 'max_age_seconds' => $maxAge,
            'rewrite_enabled' => $input['rewrite_enabled'] ?? $previous['rewrite_enabled'] ?? true];
    }
}
