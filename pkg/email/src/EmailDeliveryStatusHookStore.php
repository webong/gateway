<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\Crypt;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Resources\PlannerResourceStore;

/** One write-only signed delivery-status destination per Email account. */
final readonly class EmailDeliveryStatusHookStore
{
    private const KIND = 'email.delivery-status-hook';

    private const KEY = 'status';

    public function __construct(
        private PlannerResourceStore $resources,
        private AccountStore $accounts,
        private AccountReceivingStore $receiving,
    ) {}

    public function create(string $account, array $input): stdClass
    {
        $this->validate($input);
        if (! $this->accounts->exists($account)) {
            throw new InvalidArgumentException('Email account does not exist.');
        }
        if ($this->find($account) !== null) {
            throw new EmailDeliveryStatusHookExists('Delivery status hook already exists.');
        }

        return DB::transaction(function () use ($account, $input): stdClass {
            $row = $this->resources->create(self::KIND, self::KEY, [
                'account_id' => $account,
                'callback_url' => $input['callback_url'],
                'network_scope' => $input['network_scope'],
            ], $input['enabled'], owner: $account);
            $now = now('UTC');
            DB::table('configuration_secrets')->insert([
                'resource_id' => $row->id,
                'ciphertext' => Crypt::encryptString($input['webhook_secret']),
                'version' => 1,
                'created_at' => $now,
                'updated_at' => $now,
            ]);

            return $row;
        });
    }

    public function find(string $account): ?stdClass
    {
        return $this->resources->byOwnedKey(self::KIND, $account, self::KEY);
    }

    public function setEnabled(string $account, bool $enabled): ?stdClass
    {
        $row = $this->find($account);
        return $row === null ? null : $this->resources->replace(
            self::KIND, $row->id, self::KEY, $row->document, $enabled, $row->revision, $account,
        );
    }

    public function delete(string $account): bool
    {
        $row = $this->find($account);
        return $row !== null && $this->resources->delete(self::KIND, $row->id, $row->revision, $account);
    }

    /** @return array{id: string, callback_url: string, network_scope: string, secret: string}|null */
    public function active(string $account): ?array
    {
        $row = $this->find($account);
        if ($row === null || ! $row->enabled) {
            return null;
        }
        $ciphertext = DB::table('configuration_secrets')->where('resource_id', $row->id)->value('ciphertext');
        if (! is_string($ciphertext)) {
            return null;
        }

        return [
            'id' => $row->id,
            'callback_url' => $row->document['callback_url'],
            'network_scope' => $row->document['network_scope'],
            'secret' => Crypt::decryptString($ciphertext),
        ];
    }

    private function validate(array $input): void
    {
        $keys = array_keys($input);
        sort($keys);
        if ($keys !== ['callback_url', 'enabled', 'network_scope', 'webhook_secret']
            || ! is_string($input['callback_url'])
            || ! is_string($input['network_scope'])
            || ! in_array($input['network_scope'], ['public', 'private'], true)
            || ! is_string($input['webhook_secret'])
            || preg_match('/^[a-f0-9]{64}$/D', $input['webhook_secret']) !== 1
            || ! is_bool($input['enabled'])
            || ! $this->validURL($input['callback_url'], $input['network_scope'])) {
            throw new InvalidArgumentException('Delivery status hook is invalid.');
        }
    }

    private function validURL(string $url, string $scope): bool
    {
        if ($scope === 'public') {
            return MailboxCredentialSourceStore::safeUrl($url);
        }
        $parts = parse_url($url);
        if (! is_array($parts) || ($parts['scheme'] ?? null) !== 'https'
            || ! is_string($parts['host'] ?? null) || $parts['host'] === ''
            || isset($parts['user']) || isset($parts['pass']) || isset($parts['query']) || isset($parts['fragment'])
            || strlen($url) > 2048) {
            return false;
        }
        foreach ($this->receiving->settings()?->document['private_callbacks'] ?? [] as $grant) {
            if (($grant['url'] ?? null) === $url) {
                return true;
            }
        }

        return false;
    }
}
