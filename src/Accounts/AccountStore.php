<?php

declare(strict_types=1);

namespace Webong\Gateway\Accounts;

use Illuminate\Support\Facades\DB;
use InvalidArgumentException;

/** Shared identity for any account-owned Planner resource kind. */
final readonly class AccountStore
{
    public function create(string $id): string
    {
        if (preg_match('/^[A-Za-z0-9._:-]{1,200}$/D', $id) !== 1) {
            throw new InvalidArgumentException('Invalid account ID.');
        }

        $token = bin2hex(random_bytes(32));
        DB::table('accounts')->insert([
            'id' => $id,
            'token_hash' => hash('sha256', $token),
            'created_at' => now('UTC'),
            'updated_at' => now('UTC'),
        ]);

        return $token;
    }

    public function exists(string $id): bool
    {
        return DB::table('accounts')->where('id', $id)->exists();
    }

    public function authorized(string $id, string $token): bool
    {
        $row = DB::table('accounts')->where('id', $id)->first();

        return $row !== null && preg_match('/^[a-f0-9]{64}$/D', $token) === 1
            && hash_equals($row->token_hash, hash('sha256', $token));
    }
}
