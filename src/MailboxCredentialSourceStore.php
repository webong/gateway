<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Account-owned, registered HTTPS origins; provider tokens never live here. */
final readonly class MailboxCredentialSourceStore
{
    private const KIND = 'email.credential-source';

    public function __construct(private PlannerResourceStore $resources, private AccountStore $accounts,
        private CredentialScriptRegistry $scripts) {}

    public function create(string $account, array $input): stdClass
    {
        $this->validate($input);
        if (! $this->accounts->exists($account)) {
            throw new InvalidArgumentException('Mailbox account does not exist.');
        }

        return DB::transaction(function () use ($account, $input): stdClass {
            $row = $this->resources->create(self::KIND, $input['name'], $this->document($input), owner: $account);
            $now = now('UTC');
            DB::table('configuration_secrets')->insert([
                'resource_id' => $row->id,
                'ciphertext' => Crypt::encryptString($input['authorization']),
                'version' => 1, 'created_at' => $now, 'updated_at' => $now,
            ]);
            return $row;
        });
    }

    public function find(string $account, string $id): ?stdClass
    {
        return $this->resources->findOwned(self::KIND, $id, $account);
    }

    public function replace(string $account, string $id, array $input): ?stdClass
    {
        $this->validate($input);
        return DB::transaction(function () use ($account, $id, $input): ?stdClass {
            $current = $this->find($account, $id);
            if ($current === null) { return null; }
            $row = $this->resources->replace(self::KIND, $id, $input['name'], $this->document($input),
                true, $current->revision, $account);
            DB::table('configuration_secrets')->where('resource_id', $id)->increment('version', 1, [
                'ciphertext' => Crypt::encryptString($input['authorization']), 'updated_at' => now('UTC'),
            ]);
            return $row;
        });
    }

    /** @return list<stdClass> */
    public function all(string $account): array
    {
        return $this->resources->allOwned(self::KIND, $account);
    }

    public function delete(string $account, string $id): bool
    {
        $row = $this->find($account, $id);
        return $row !== null && $this->resources->delete(self::KIND, $id, $row->revision, $account);
    }

    /** @return array{url: string, adapter: string, authorization: string, language?: string, source?: string}|null */
    public function active(string $account, string $id): ?array
    {
        $row = $this->find($account, $id);
        if ($row === null || ! $row->enabled) { return null; }
        $secret = DB::table('configuration_secrets')->where('resource_id', $id)->value('ciphertext');
        if (! is_string($secret)) { return null; }
        return array_filter(['url' => $row->document['url'], 'adapter' => $row->document['adapter'] ?? 'http',
            'authorization' => Crypt::decryptString($secret), 'language' => $row->document['language'] ?? null,
            'source' => $row->document['source'] ?? null], static fn (mixed $value): bool => $value !== null);
    }

    private function validate(array $input): void
    {
        $keys = array_keys($input);
        sort($keys);
        $adapter = $input['adapter'] ?? 'http';
        $validKeys = $adapter === 'script'
            ? ['adapter', 'authorization', 'language', 'name', 'source', 'url']
            : ['adapter', 'authorization', 'name', 'url'];
        $legacyKeys = ['authorization', 'name', 'url'];
        if (! in_array($keys, [$validKeys, $legacyKeys], true) ||
            ! is_string($input['name']) || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $input['name']) !== 1 ||
            ! is_string($adapter) ||
            ! array_key_exists($adapter,
                (array) config('gateway.email.credential_source_adapters', [])) ||
            ! is_string($input['authorization']) || strlen($input['authorization']) < 32 ||
            strlen($input['authorization']) > 512 || preg_match('/[^\x21-\x7E]/', $input['authorization']) === 1 ||
            ! is_string($input['url']) || ! self::safeUrl($input['url'])) {
            throw new InvalidArgumentException('Mailbox credential source is invalid.');
        }
        if ($adapter === 'script' && (! is_string($input['language']) || ! $this->scripts->supports($input['language']) ||
            ! is_string($input['source']) || trim($input['source']) === '' || strlen($input['source']) > 65536)) {
            throw new InvalidArgumentException('Mailbox credential source is invalid.');
        }
    }

    /** @return array<string, string> */
    private function document(array $input): array
    {
        return array_filter(['url' => $input['url'], 'adapter' => $input['adapter'] ?? 'http',
            'language' => $input['language'] ?? null, 'source' => $input['source'] ?? null],
            static fn (mixed $value): bool => $value !== null);
    }

    public static function safeUrl(string $url): bool
    {
        $parts = parse_url($url);
        return is_array($parts) && ($parts['scheme'] ?? null) === 'https' &&
            is_string($parts['host'] ?? null) && strlen($parts['host']) <= 253 &&
            preg_match('/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/D', $parts['host']) === 1 &&
            str_contains($parts['host'], '.') && ! str_ends_with($parts['host'], '.local') &&
            filter_var($parts['host'], FILTER_VALIDATE_IP) === false &&
            (! isset($parts['port']) || $parts['port'] === 443) &&
            ! isset($parts['user']) && ! isset($parts['pass']) && ! isset($parts['fragment']) &&
            ! isset($parts['query']) && strlen($url) <= 2048;
    }
}
