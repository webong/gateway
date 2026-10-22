<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use InvalidArgumentException;
use RuntimeException;
use stdClass;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Account-owned definitions are audited resources; live credentials and test results are not. */
final readonly class MailboxConnectionStore
{
    private const KIND = 'email.mailbox-connection';

    public function __construct(private ?MailboxOAuthRefresher $refresher,
        private ?EmailUpstreamCatalog $upstreams, private PlannerResourceStore $resources,
        private AccountStore $accounts, private ?ApplicationMailboxGrantSource $applicationGrants = null,
        private ?MailboxCredentialSourceStore $credentialSources = null) {}

    public function find(string $account, string $id): ?stdClass
    {
        return $this->record($this->resources->findOwned(self::KIND, $id, $account));
    }

    /** @return list<stdClass> */
    public function all(string $account): array
    {
        return array_map($this->record(...), $this->resources->allOwned(self::KIND, $account));
    }

    /** @param array<string, string> $input
     * @return array{record: stdClass, imap_password: ?string, smtp_password: ?string}
     */
    public function create(string $account, array $input): array
    {
        $this->validate($input);
        return $this->createRecord($account, $input, 'mailbox');
    }

    /** SMTP-only account connection; it never grants IMAP access. */
    public function createSending(string $account, array $input): array
    {
        $this->validateSending($input);
        return $this->createRecord($account, $input, 'sending');
    }

    /** IMAP-only account connection; it never grants SMTP submission. */
    public function createImap(string $account, array $input): array
    {
        $this->validateImap($input);
        return $this->createRecord($account, $input, 'imap');
    }

    private function validateSending(array $input): void
    {
        if (($input['credential_mode'] ?? null) === 'application_managed') {
            $this->validateApplication($input, 'sending');
            return;
        }
        $keys = ['address', 'smtp_upstream_id', 'smtp_username', 'smtp_auth', 'smtp_secret'];
        $provided = array_keys($input);
        sort($keys);
        sort($provided);
        if ($provided !== $keys || count(array_filter($input, 'is_string')) !== 5
            || ! filter_var($input['address'], FILTER_VALIDATE_EMAIL)
            || strlen($input['address']) > 320 || $this->upstreams?->active('smtp', $input['smtp_upstream_id']) === null
            || ! in_array($input['smtp_auth'], ['password', 'google', 'microsoft'], true)
            || $input['smtp_username'] === '' || strlen($input['smtp_username']) > 320
            || preg_match('/[\x00-\x1F\x7F]/', $input['smtp_username']) === 1
            || $input['smtp_secret'] === '' || strlen($input['smtp_secret']) > 8192) {
            throw new InvalidArgumentException('Sending connection is invalid.');
        }
    }

    private function validateImap(array $input): void
    {
        if (($input['credential_mode'] ?? null) === 'application_managed') {
            $this->validateApplication($input, 'imap');
            return;
        }
        $keys = ['address', 'imap_upstream_id', 'imap_username', 'imap_auth', 'imap_secret'];
        $provided = array_keys($input);
        sort($keys);
        sort($provided);
        if ($provided !== $keys || count(array_filter($input, 'is_string')) !== 5
            || ! filter_var($input['address'], FILTER_VALIDATE_EMAIL)
            || strlen($input['address']) > 320 || $this->upstreams?->active('imap', $input['imap_upstream_id']) === null
            || ! in_array($input['imap_auth'], ['password', 'google', 'microsoft'], true)
            || $input['imap_username'] === '' || strlen($input['imap_username']) > 320
            || preg_match('/[\x00-\x1F\x7F]/', $input['imap_username']) === 1
            || $input['imap_secret'] === '' || strlen($input['imap_secret']) > 8192) {
            throw new InvalidArgumentException('IMAP connection is invalid.');
        }
    }

    private function createRecord(string $account, array $input, string $mode): array
    {
        if (! $this->accounts->exists($account)) {
            throw new InvalidArgumentException('Mailbox account does not exist.');
        }
        $this->assertCredentialSource($account, $input);
        $imapPassword = $mode !== 'sending' ? bin2hex(random_bytes(32)) : null;
        $smtpPassword = $mode !== 'imap' ? bin2hex(random_bytes(32)) : null;

        return DB::transaction(function () use ($account, $input, $mode, $imapPassword, $smtpPassword): array {
            $row = $this->resources->create(self::KIND, $this->resourceKey($input['address'], $mode), $this->document($input, $mode),
                false, owner: $account);
            $now = now('UTC');
            DB::table('configuration_secrets')->insert([
                'resource_id' => $row->id,
                'ciphertext' => $this->encodeSecrets([
                    'imap_secret' => $input['imap_secret'] ?? null, 'smtp_secret' => $input['smtp_secret'] ?? null,
                    'imap_client_hash' => $imapPassword !== null ? password_hash($imapPassword, PASSWORD_DEFAULT) : null,
                    'smtp_client_hash' => $smtpPassword !== null ? password_hash($smtpPassword, PASSWORD_DEFAULT) : null,
                ]),
                'version' => 1, 'created_at' => $now, 'updated_at' => $now,
            ]);
            DB::table('resource_observations')->insert([
                'resource_id' => $row->id, 'state' => 'untested', 'observed_revision' => null,
                'observed_at' => null, 'created_at' => $now, 'updated_at' => $now,
            ]);

            return ['record' => $this->find($account, $row->id), 'imap_password' => $imapPassword,
                'smtp_password' => $smtpPassword];
        });
    }

    public function authenticate(string $id, string $password, string $side): ?stdClass
    {
        if (! in_array($side, ['imap', 'smtp'], true) || ! Str::isUuid($id)) { return null; }
        $row = $this->resources->findAny(self::KIND, $id);
        $mode = $row?->document['mode'] ?? 'mailbox';
        $secrets = $row === null || ! $row->enabled || ($side === 'smtp' && $mode === 'imap')
            || ($side === 'imap' && $mode === 'sending') ? null : $this->secrets($id);

        return $secrets !== null && is_string($secrets[$side.'_client_hash'] ?? null)
            && password_verify($password, $secrets[$side.'_client_hash'])
            ? $this->record($row) : null;
    }

    /** @return array<string, string>|null */
    public function resolve(string $id, string $side, bool $allowDisabled = false, int $minTtlSeconds = 30): ?array
    {
        if (! in_array($side, ['imap', 'smtp'], true) || ! Str::isUuid($id)) { return null; }
        $row = $this->resources->findAny(self::KIND, $id);
        if ($row === null || (! $allowDisabled && ! $row->enabled)) { return null; }
        if (($side === 'imap' && ($row->document['mode'] ?? 'mailbox') === 'sending')
            || ($side === 'smtp' && ($row->document['mode'] ?? 'mailbox') === 'imap')) { return null; }
        $secretRow = DB::table('configuration_secrets')->where('resource_id', $id)->first();
        if ($secretRow === null) { return null; }
        $secrets = $this->decodeSecrets($secretRow->ciphertext);
        $secret = $secrets[$side.'_secret'];
        $auth = $row->document[$side.'_auth'];
        if (($row->document['credential_mode'] ?? 'gateway_managed') === 'application_managed') {
            if ($auth !== 'xoauth2' || $this->applicationGrants === null ||
                ! is_string($row->document['credential_source_id'] ?? null)) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            if ($this->credentialSources?->active($row->owner, $row->document['credential_source_id']) === null) {
                throw new RuntimeException('Mailbox credential source is unavailable.');
            }
            $grant = $this->applicationGrants->resolve($row->document['credential_source_id'],
                $row->owner, $id, $side, $row->document['address'] ?? $row->key);
            $keys = array_keys($grant);
            sort($keys);
            if ($keys !== ['access_token', 'account_id', 'address', 'capabilities', 'connection_id', 'expires_at'] ||
                ($grant['account_id'] ?? null) !== $row->owner || ($grant['connection_id'] ?? null) !== $id ||
                ! is_string($grant['address']) ||
                strcasecmp($grant['address'], $row->document['address'] ?? $row->key) !== 0 ||
                ! is_array($grant['capabilities']) || ! array_is_list($grant['capabilities']) ||
                count($grant['capabilities']) > 2 ||
                count(array_filter($grant['capabilities'], static fn (mixed $capability): bool =>
                    is_string($capability) && in_array($capability, ['imap', 'smtp'], true))) !== count($grant['capabilities']) ||
                ! in_array($side, $grant['capabilities'], true) ||
                ! is_int($grant['expires_at']) || $grant['expires_at'] <= time() + $minTtlSeconds ||
                $grant['expires_at'] > time() + 3600 ||
                ! is_string($grant['access_token']) || $grant['access_token'] === '' ||
                strlen($grant['access_token']) > 4096 || preg_match('/[^\x20-\x7E]/', $grant['access_token']) === 1) {
                throw new RuntimeException('Mailbox access grant is invalid.');
            }
            $secret = $grant['access_token'];
        } elseif ($auth !== 'password') {
            if (! in_array($auth, ['google', 'microsoft'], true) || $this->refresher === null) {
                throw new RuntimeException('Mailbox OAuth refresh is unavailable.');
            }
            $tokens = $this->refresher->refresh($auth, $side, $secret);
            $access = $tokens['access_token'] ?? null;
            if (! is_string($access) || $access === '' || strlen($access) > 4096 || preg_match('/[^\x20-\x7E]/', $access) === 1) {
                throw new RuntimeException('Mailbox OAuth token is invalid.');
            }
            $rotated = $tokens['refresh_token'] ?? null;
            if (is_string($rotated) && $rotated !== '' && strlen($rotated) <= 8192) {
                $this->rotateRefreshToken($id, $side, $secret, $rotated);
            }
            $secret = $access;
        }

        return ['account_id' => $row->owner, 'address' => $row->document['address'] ?? $row->key,
            'mode' => $row->document['mode'] ?? 'mailbox',
            'upstream_id' => $row->document[$side.'_upstream_id'], 'username' => $row->document[$side.'_username'],
            'secret' => $secret, 'mechanism' => $auth === 'password' ? 'PLAIN' : 'XOAUTH2'];
    }

    public function update(string $account, string $id, bool $enabled): ?stdClass
    {
        return DB::transaction(function () use ($account, $id, $enabled): ?stdClass {
            $row = $this->resources->findOwned(self::KIND, $id, $account);
            if ($row === null) { return null; }
            $observation = DB::table('resource_observations')->where('resource_id', $id)->first();
            if ($enabled && ($observation?->state !== 'healthy' || (int) $observation->observed_revision !== $row->revision)) {
                throw new RuntimeException('Test both transports first.');
            }
            $updated = $this->resources->replace(self::KIND, $id, $row->key, $row->document,
                $enabled, $row->revision, $account);
            if ($observation?->observed_revision !== null) {
                DB::table('resource_observations')->where('resource_id', $id)
                    ->update(['observed_revision' => $updated->revision, 'updated_at' => now('UTC')]);
            }

            return $this->record($updated);
        });
    }

    /** Replace upstream settings and secrets while disabled; re-test before re-enabling. */
    public function replace(string $account, string $id, array $input): ?stdClass
    {
        $this->validate($input);
        return $this->replaceRecord($account, $id, $input, 'mailbox');
    }

    public function replaceSending(string $account, string $id, array $input): ?stdClass
    {
        $this->validateSending($input);
        return $this->replaceRecord($account, $id, $input, 'sending');
    }

    public function replaceImap(string $account, string $id, array $input): ?stdClass
    {
        $this->validateImap($input);
        return $this->replaceRecord($account, $id, $input, 'imap');
    }

    private function replaceRecord(string $account, string $id, array $input, string $mode): ?stdClass
    {
        $this->assertCredentialSource($account, $input);
        return DB::transaction(function () use ($account, $id, $input, $mode): ?stdClass {
            $row = $this->resources->findOwned(self::KIND, $id, $account);
            if ($row === null || ($row->document['mode'] ?? 'mailbox') !== $mode) { return null; }
            $currentSecrets = $this->secrets($id);
            if ($currentSecrets === null) { throw new RuntimeException('Mailbox credentials are unavailable.'); }
            $updated = $this->resources->replace(self::KIND, $id, $this->resourceKey($input['address'], $mode),
                $this->document($input, $mode), false, $row->revision, $account);
            DB::table('configuration_secrets')->where('resource_id', $id)->increment('version', 1, [
                'ciphertext' => $this->encodeSecrets([
                    'imap_secret' => $input['imap_secret'] ?? null, 'smtp_secret' => $input['smtp_secret'] ?? null,
                    'imap_client_hash' => $currentSecrets['imap_client_hash'],
                    'smtp_client_hash' => $currentSecrets['smtp_client_hash'],
                ]), 'updated_at' => now('UTC'),
            ]);
            DB::table('resource_observations')->where('resource_id', $id)->update([
                'state' => 'untested', 'observed_revision' => null, 'observed_at' => null, 'updated_at' => now('UTC'),
            ]);

            return $this->record($updated);
        });
    }

    public function rotateSMTPClientPassword(string $account, string $id): ?string
    {
        return DB::transaction(function () use ($account, $id): ?string {
            $row = $this->resources->findOwned(self::KIND, $id, $account);
            if ($row === null || ($row->document['mode'] ?? 'mailbox') !== 'sending') { return null; }
            $secrets = $this->secrets($id);
            if ($secrets === null) { throw new RuntimeException('Sending credentials are unavailable.'); }
            $password = bin2hex(random_bytes(32));
            $secrets['smtp_client_hash'] = password_hash($password, PASSWORD_DEFAULT);
            DB::table('configuration_secrets')->where('resource_id', $id)->increment('version', 1, [
                'ciphertext' => $this->encodeSecrets($secrets), 'updated_at' => now('UTC'),
            ]);
            return $password;
        });
    }

    public function rotateIMAPClientPassword(string $account, string $id): ?string
    {
        return DB::transaction(function () use ($account, $id): ?string {
            $row = $this->resources->findOwned(self::KIND, $id, $account);
            if ($row === null || ($row->document['mode'] ?? 'mailbox') !== 'imap') { return null; }
            $secrets = $this->secrets($id);
            if ($secrets === null) { throw new RuntimeException('IMAP credentials are unavailable.'); }
            $password = bin2hex(random_bytes(32));
            $secrets['imap_client_hash'] = password_hash($password, PASSWORD_DEFAULT);
            DB::table('configuration_secrets')->where('resource_id', $id)->increment('version', 1, [
                'ciphertext' => $this->encodeSecrets($secrets), 'updated_at' => now('UTC'),
            ]);
            return $password;
        });
    }

    public function markHealth(string $account, string $id, bool $healthy): ?stdClass
    {
        return DB::transaction(function () use ($account, $id, $healthy): ?stdClass {
            $row = $this->resources->findOwned(self::KIND, $id, $account);
            if ($row === null) { return null; }
            DB::table('resource_observations')->where('resource_id', $id)->update([
                'state' => $healthy ? 'healthy' : 'unhealthy', 'observed_revision' => $row->revision,
                'observed_at' => now('UTC'), 'updated_at' => now('UTC'),
            ]);

            return $this->record($row);
        });
    }

    public function delete(string $account, string $id): bool
    {
        return DB::transaction(function () use ($account, $id): bool {
            $row = $this->resources->findOwned(self::KIND, $id, $account);

            return $row !== null && $this->resources->delete(self::KIND, $id, $row->revision, $account);
        });
    }

    /** @param array<string, string> $input
     * @return array<string, string>
     */
    private function document(array $input, string $mode = 'mailbox'): array
    {
        return [
            'mode' => $mode,
            'credential_mode' => $input['credential_mode'] ?? 'gateway_managed',
            'credential_source_id' => $input['credential_source_id'] ?? null,
            ...($mode === 'imap' ? ['address' => strtolower($input['address'])] : []),
            'imap_upstream_id' => $input['imap_upstream_id'] ?? null, 'smtp_upstream_id' => $input['smtp_upstream_id'] ?? null,
            'imap_username' => $input['imap_username'] ?? null, 'smtp_username' => $input['smtp_username'] ?? null,
            'imap_auth' => $input['imap_auth'] ?? null, 'smtp_auth' => $input['smtp_auth'] ?? null,
        ];
    }

    private function record(?stdClass $resource): ?stdClass
    {
        if ($resource === null) { return null; }
        $observation = DB::table('resource_observations')->where('resource_id', $resource->id)->first();

        return (object) [
            'id' => $resource->id, 'account_id' => $resource->owner, 'address' => $resource->document['address'] ?? $resource->key,
            'mode' => $resource->document['mode'] ?? 'mailbox', ...$resource->document, 'enabled' => $resource->enabled,
            'health' => $observation?->state ?? 'untested', 'tested_at' => $observation?->observed_at,
            'revision' => $resource->revision,
        ];
    }

    /** @return array<string, string>|null */
    private function secrets(string $id): ?array
    {
        $row = DB::table('configuration_secrets')->where('resource_id', $id)->first();

        return $row === null ? null : $this->decodeSecrets($row->ciphertext);
    }

    private function resourceKey(string $address, string $mode): string
    {
        $address = strtolower($address);
        return $mode === 'imap' ? 'imap:'.$address : $address;
    }

    /** @param array<string, string> $secrets */
    private function encodeSecrets(array $secrets): string
    {
        return Crypt::encryptString(json_encode($secrets, JSON_THROW_ON_ERROR));
    }

    /** @return array<string, string> */
    private function decodeSecrets(string $ciphertext): array
    {
        return json_decode(Crypt::decryptString($ciphertext), true, 512, JSON_THROW_ON_ERROR);
    }

    /** Merge side-specific rotations without overwriting a concurrent rotation or replacement. */
    private function rotateRefreshToken(string $id, string $side, string $old, string $rotated): void
    {
        for ($attempt = 0; $attempt < 3; $attempt++) {
            $row = DB::table('configuration_secrets')->where('resource_id', $id)->first();
            if ($row === null) { return; }
            $secrets = $this->decodeSecrets($row->ciphertext);
            if ($secrets[$side.'_secret'] !== $old) { return; }
            $secrets[$side.'_secret'] = $rotated;
            // A combined OAuth grant starts with one refresh token for both
            // protocols. Keep both sides on the newest rotation when shared.
            $other = $side === 'imap' ? 'smtp' : 'imap';
            $connection = $this->resources->findAny(self::KIND, $id);
            if (($connection?->document[$other.'_auth'] ?? null) === ($connection?->document[$side.'_auth'] ?? null)
                && ($secrets[$other.'_secret'] ?? null) === $old) {
                $secrets[$other.'_secret'] = $rotated;
            }
            $updated = DB::table('configuration_secrets')->where('resource_id', $id)->where('version', $row->version)
                ->increment('version', 1, ['ciphertext' => $this->encodeSecrets($secrets), 'updated_at' => now('UTC')]);
            if ($updated === 1) { return; }
        }
    }

    /** @param array<string, string> $input */
    private function validate(array $input): void
    {
        if (($input['credential_mode'] ?? null) === 'application_managed') {
            $this->validateApplication($input, 'mailbox');
            return;
        }
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

    /** @param array<string, mixed> $input */
    private function validateApplication(array $input, string $mode): void
    {
        $sides = $mode === 'mailbox' ? ['imap', 'smtp'] : [$mode === 'sending' ? 'smtp' : 'imap'];
        $keys = ['address', 'credential_mode', 'credential_source_id'];
        foreach ($sides as $side) {
            array_push($keys, $side.'_upstream_id', $side.'_username', $side.'_auth');
        }
        sort($keys);
        $provided = array_keys($input);
        sort($provided);
        if ($keys !== $provided || ! is_string($input['address'] ?? null) ||
            ! filter_var($input['address'], FILTER_VALIDATE_EMAIL) || strlen($input['address']) > 320 ||
            ! is_string($input['credential_source_id'] ?? null) ||
            ! Str::isUuid($input['credential_source_id'])) {
            throw new InvalidArgumentException('Application-managed mailbox connection is invalid.');
        }
        foreach ($sides as $side) {
            if (! is_string($input[$side.'_upstream_id']) ||
                $this->upstreams?->active($side, $input[$side.'_upstream_id']) === null ||
                $input[$side.'_auth'] !== 'xoauth2' ||
                ! is_string($input[$side.'_username']) ||
                strcasecmp($input[$side.'_username'], $input['address']) !== 0) {
                throw new InvalidArgumentException('Application-managed mailbox upstream is invalid.');
            }
        }
    }

    /** @param array<string, mixed> $input */
    private function assertCredentialSource(string $account, array $input): void
    {
        if (($input['credential_mode'] ?? 'gateway_managed') === 'application_managed' &&
            $this->credentialSources?->active($account, $input['credential_source_id']) === null) {
            throw new InvalidArgumentException('Credential source does not belong to this account.');
        }
    }
}
