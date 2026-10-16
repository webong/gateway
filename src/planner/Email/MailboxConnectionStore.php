<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use InvalidArgumentException;
use RuntimeException;
use stdClass;

/** Secrets are encrypted with Laravel's application key and never returned by resources. */
final readonly class MailboxConnectionStore
{
    public function __construct(private ?MailboxOAuthRefresher $refresher = null,
        private ?EmailUpstreamCatalog $upstreams = null) {}

    public function createAccount(string $id): string
    {
        if (preg_match('/^[A-Za-z0-9._:-]{1,200}$/D', $id) !== 1) { throw new InvalidArgumentException('Invalid account ID.'); }
        $token = bin2hex(random_bytes(32));
        DB::table('mailbox_accounts')->insert(['id' => $id, 'token_hash' => hash('sha256', $token),
            'created_at' => now('UTC'), 'updated_at' => now('UTC')]);

        return $token;
    }

    public function authorized(string $account, string $token): bool
    {
        $row = DB::table('mailbox_accounts')->where('id', $account)->first();

        return $row !== null && preg_match('/^[a-f0-9]{64}$/D', $token) === 1
            && hash_equals($row->token_hash, hash('sha256', $token));
    }

    public function find(string $account, string $id): ?stdClass
    {
        return DB::table('mailbox_connections')->where('account_id', $account)->where('id', $id)->first();
    }

    /** @return list<stdClass> */
    public function all(string $account): array
    {
        return DB::table('mailbox_connections')->where('account_id', $account)->orderBy('created_at')->get()->all();
    }

    /** @param array<string, string> $input
     * @return array{record: stdClass, imap_password: string, smtp_password: string}
     */
    public function create(string $account, array $input): array
    {
        $this->validate($input);
        $id = (string) Str::uuid();
        $imapPassword = bin2hex(random_bytes(32));
        $smtpPassword = bin2hex(random_bytes(32));
        $now = now('UTC');
        DB::table('mailbox_connections')->insert([
            'id' => $id, 'account_id' => $account, 'address' => strtolower($input['address']),
            'imap_upstream_id' => $input['imap_upstream_id'], 'smtp_upstream_id' => $input['smtp_upstream_id'],
            'imap_username' => $input['imap_username'], 'smtp_username' => $input['smtp_username'],
            'imap_auth' => $input['imap_auth'], 'smtp_auth' => $input['smtp_auth'],
            'imap_secret' => Crypt::encryptString($input['imap_secret']),
            'smtp_secret' => Crypt::encryptString($input['smtp_secret']),
            'imap_client_hash' => password_hash($imapPassword, PASSWORD_DEFAULT),
            'smtp_client_hash' => password_hash($smtpPassword, PASSWORD_DEFAULT),
            'enabled' => false, 'health' => 'untested', 'created_at' => $now, 'updated_at' => $now,
        ]);

        return ['record' => $this->find($account, $id), 'imap_password' => $imapPassword,
            'smtp_password' => $smtpPassword];
    }

    public function authenticate(string $id, string $password, string $side): ?stdClass
    {
        if (! in_array($side, ['imap', 'smtp'], true) || ! Str::isUuid($id)) { return null; }
        $row = DB::table('mailbox_connections')->where('id', $id)->where('enabled', true)->first();

        return $row !== null && password_verify($password, $row->{$side.'_client_hash'}) ? $row : null;
    }

    public function resolve(string $id, string $side, bool $allowDisabled = false): ?array
    {
        if (! in_array($side, ['imap', 'smtp'], true) || ! Str::isUuid($id)) { return null; }
        $query = DB::table('mailbox_connections')->where('id', $id);
        if (! $allowDisabled) { $query->where('enabled', true); }
        $row = $query->first();
        if ($row === null) { return null; }
        $secret = Crypt::decryptString($row->{$side.'_secret'});
        if ($row->{$side.'_auth'} !== 'password') {
            if (! in_array($row->{$side.'_auth'}, ['google', 'microsoft'], true) || $this->refresher === null) {
                throw new RuntimeException('Mailbox OAuth refresh is unavailable.');
            }
            $tokens = $this->refresher->refresh($row->{$side.'_auth'}, $side, $secret);
            $access = $tokens['access_token'] ?? null;
            if (! is_string($access) || $access === '' || strlen($access) > 4096 || preg_match('/[^\x20-\x7E]/', $access) === 1) {
                throw new RuntimeException('Mailbox OAuth token is invalid.');
            }
            $rotated = $tokens['refresh_token'] ?? null;
            if (is_string($rotated) && $rotated !== '' && strlen($rotated) <= 8192) {
                DB::table('mailbox_connections')->where('id', $id)->where("{$side}_secret", $row->{$side.'_secret'})
                    ->update(["{$side}_secret" => Crypt::encryptString($rotated), 'updated_at' => now('UTC')]);
            }
            $secret = $access;
        }

        return ['account_id' => $row->account_id, 'address' => $row->address,
            'upstream_id' => $row->{$side.'_upstream_id'}, 'username' => $row->{$side.'_username'},
            'secret' => $secret, 'mechanism' => $row->{$side.'_auth'} === 'password' ? 'PLAIN' : 'XOAUTH2'];
    }

    public function update(string $account, string $id, bool $enabled): ?stdClass
    {
        $row = $this->find($account, $id);
        if ($row === null) { return null; }
        if ($enabled && $row->health !== 'healthy') { throw new RuntimeException('Test both transports first.'); }
        DB::table('mailbox_connections')->where('account_id', $account)->where('id', $id)
            ->update(['enabled' => $enabled, 'updated_at' => now('UTC')]);

        return $this->find($account, $id);
    }

    /** Replace upstream settings and secrets while disabled; re-test before re-enabling. */
    public function replace(string $account, string $id, array $input): ?stdClass
    {
        $this->validate($input);
        if ($this->find($account, $id) === null) { return null; }
        DB::table('mailbox_connections')->where('account_id', $account)->where('id', $id)->update([
            'address' => strtolower($input['address']),
            'imap_upstream_id' => $input['imap_upstream_id'], 'smtp_upstream_id' => $input['smtp_upstream_id'],
            'imap_username' => $input['imap_username'], 'smtp_username' => $input['smtp_username'],
            'imap_auth' => $input['imap_auth'], 'smtp_auth' => $input['smtp_auth'],
            'imap_secret' => Crypt::encryptString($input['imap_secret']),
            'smtp_secret' => Crypt::encryptString($input['smtp_secret']),
            'enabled' => false, 'health' => 'untested', 'tested_at' => null, 'updated_at' => now('UTC'),
        ]);

        return $this->find($account, $id);
    }

    public function markHealth(string $account, string $id, bool $healthy): ?stdClass
    {
        if ($this->find($account, $id) === null) { return null; }
        DB::table('mailbox_connections')->where('account_id', $account)->where('id', $id)
            ->update(['health' => $healthy ? 'healthy' : 'unhealthy', 'tested_at' => now('UTC'),
                'updated_at' => now('UTC')]);

        return $this->find($account, $id);
    }

    public function delete(string $account, string $id): bool
    {
        return DB::table('mailbox_connections')->where('account_id', $account)->where('id', $id)->delete() === 1;
    }

    /** @param array<string, string> $input */
    private function validate(array $input): void
    {
        $keys = ['address', 'imap_upstream_id', 'smtp_upstream_id', 'imap_username', 'smtp_username',
            'imap_auth', 'smtp_auth', 'imap_secret', 'smtp_secret'];
        sort($keys);
        $provided = array_keys($input);
        sort($provided);
        if ($keys !== $provided || count(array_filter($input, 'is_string')) !== 9
            || strlen($input['address']) > 320 || ! filter_var($input['address'], FILTER_VALIDATE_EMAIL)) {
            throw new InvalidArgumentException('Mailbox connection is invalid.');
        }
        foreach (['imap', 'smtp'] as $side) {
            $id = $input[$side.'_upstream_id'];
            if ($this->upstreams?->active($side, $id) === null
                || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $id) !== 1
                || ! in_array($input[$side.'_auth'], ['password', 'google', 'microsoft'], true)
                || $input[$side.'_username'] === '' || strlen($input[$side.'_username']) > 320
                || preg_match('/[\x00-\x1F\x7F]/', $input[$side.'_username']) === 1
                || $input[$side.'_secret'] === '' || strlen($input[$side.'_secret']) > 8192) {
                throw new InvalidArgumentException('Mailbox upstream is not approved or its credentials are invalid.');
            }
        }
    }
}
