<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;

/** Operator-managed destinations that must not receive new outbound mail. */
final readonly class EmailSuppressionStore
{
    /** @return list<stdClass> */
    public function entries(?string $cursor = null, int $limit = 100): array
    {
        if (($cursor !== null && preg_match('/\A[a-f0-9]{64}\z/D', $cursor) !== 1) || $limit < 1 || $limit > 100) {
            throw new InvalidArgumentException('Invalid Email suppression page.');
        }

        return DB::table('email_suppressions')
            ->when($cursor !== null, static fn ($query) => $query->where('id', '>', $cursor))
            ->orderBy('id')->limit($limit + 1)->get()->all();
    }

    public function suppress(string $address, string $reason): stdClass
    {
        $address = self::canonical($address);
        if (! in_array($reason, ['manual', 'hard_bounce', 'complaint'], true)) {
            throw new InvalidArgumentException('Invalid Email suppression reason.');
        }
        $id = hash('sha256', $address);
        $now = now('UTC');
        DB::table('email_suppressions')->upsert([[
            'id' => $id,
            'address' => $address,
            'reason' => $reason,
            'created_at' => $now,
            'updated_at' => $now,
        ]], ['id'], ['reason', 'updated_at']);

        return DB::table('email_suppressions')->where('id', $id)->first();
    }

    public function remove(string $id): bool
    {
        return preg_match('/\A[a-f0-9]{64}\z/D', $id) === 1
            && DB::table('email_suppressions')->where('id', $id)->delete() === 1;
    }

    /** @param list<string> $addresses */
    public function containsAny(array $addresses): bool
    {
        $ids = [];
        foreach ($addresses as $address) {
            try {
                $ids[] = hash('sha256', self::canonical($address));
            } catch (InvalidArgumentException) {
                // Another layer validates malformed envelope addresses.
            }
        }

        return $ids !== [] && DB::table('email_suppressions')->whereIn('id', array_unique($ids))->exists();
    }

    private static function canonical(string $address): string
    {
        if (strlen($address) > 254 || filter_var($address, FILTER_VALIDATE_EMAIL) === false) {
            throw new InvalidArgumentException('A valid Email address is required.');
        }
        $separator = strrpos($address, '@');

        return substr($address, 0, $separator).'@'.strtolower(substr($address, $separator + 1));
    }
}
