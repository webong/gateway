<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;
use InvalidArgumentException;
use stdClass;
use Throwable;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Gateway\Schedules\ScheduleStore;

/** Planner-owned poll definition; the generic checkpoint is runtime state, not desired state. */
final readonly class MailboxPollStore
{
    private const KIND = 'email.mailbox-poll';

    public function __construct(private PlannerResourceStore $resources, private MailboxConnectionStore $connections,
        private EmailUpstreamCatalog $upstreams, private ScheduleStore $schedules) {}

    public function find(string $account, string $connection): ?stdClass
    {
        return $this->resources->byOwnedKey(self::KIND, $account, $connection);
    }

    public function delete(string $account, string $connection): bool
    {
        return DB::transaction(function () use ($account, $connection): bool {
            $row = $this->find($account, $connection);
            if ($row === null) { return false; }
            DB::table('schedules')->where('origin', 'mailbox-poll:'.$row->id)->delete();
            return $this->resources->delete(self::KIND, $row->id, $row->revision, $account);
        });
    }

    /** @param array<string, mixed> $input */
    public function configure(string $account, string $connection, array $input): ?stdClass
    {
        if (array_diff(array_keys($input), ['callback_url', 'webhook_secret', 'enabled']) !== []
            || ! is_string($input['callback_url'] ?? null) || ! is_string($input['webhook_secret'] ?? null)
            || ! is_bool($input['enabled'] ?? null) || ! $this->validCallback($input['callback_url'])
            || preg_match('/\A[a-fA-F0-9]{64}\z/D', $input['webhook_secret']) !== 1) {
            throw new InvalidArgumentException('Invalid mailbox poll configuration.');
        }
        $mailbox = $this->connections->find($account, $connection);
        if ($mailbox === null || ! in_array($mailbox->mode, ['mailbox', 'imap'], true)) { return null; }
        if ($input['enabled'] && ! $mailbox->enabled) {
            throw new InvalidArgumentException('Test and enable the mailbox connection first.');
        }
        return DB::transaction(function () use ($account, $connection, $input, $mailbox): stdClass {
            $current = $this->find($account, $connection);
            $document = ['connection_id' => $connection, 'callback_url' => $input['callback_url'],
                'webhook_secret' => strtolower($input['webhook_secret'])];
            $row = $current === null
                ? $this->resources->create(self::KIND, $connection, $document, $input['enabled'], owner: $account)
                : $this->resources->replace(self::KIND, $current->id, $connection, $document,
                    $input['enabled'], $current->revision, $account);
            $now = now('UTC');
            if ($current === null) {
                DB::table('resource_checkpoints')->insert(['resource_id' => $row->id,
                    'cursor' => json_encode(['uid_validity' => null, 'last_uid' => 0], JSON_THROW_ON_ERROR),
                    'created_at' => $now, 'updated_at' => $now]);
                DB::table('schedules')->insert(['id' => (string) Str::uuid(), 'origin' => 'mailbox-poll:'.$row->id,
                    'key' => 'poll', 'name' => 'IMAP '.$mailbox->address, 'cron' => '* * * * *', 'timezone' => 'UTC',
                    'language' => 'native.imap', 'source' => $row->id, 'payload' => '{}',
                    'enabled' => $input['enabled'], 'next_run_at' => $this->schedules->nextRun('* * * * *', 'UTC'),
                    'created_at' => $now, 'updated_at' => $now]);
            } else {
                DB::table('schedules')->where('origin', 'mailbox-poll:'.$row->id)->update([
                    'enabled' => $input['enabled'], 'updated_at' => $now]);
            }
            return $row;
        });
    }

    /** A schedule claim obtains a separate resource lease before reading credentials. */
    public function claim(string $id): ?array
    {
        $lease = DB::transaction(function () use ($id): ?array {
            $poll = $this->resources->findAny(self::KIND, $id);
            if ($poll === null || ! $poll->enabled || $poll->owner === null) { return null; }
            $mailbox = $this->connections->find($poll->owner, $poll->document['connection_id']);
            if ($mailbox === null || ! $mailbox->enabled || ! in_array($mailbox->mode, ['mailbox', 'imap'], true)) { return null; }
            $checkpoint = DB::table('resource_checkpoints')->where('resource_id', $id)->lockForUpdate()->first();
            if ($checkpoint === null || ($checkpoint->lease_until !== null && $checkpoint->lease_until > now('UTC'))) {
                return null;
            }
            $token = (string) Str::uuid();
            DB::table('resource_checkpoints')->where('resource_id', $id)->update([
                'lease_token' => $token, 'lease_until' => now('UTC')->addSeconds(110), 'updated_at' => now('UTC')]);
            return ['route_id' => $poll->id, 'connection_id' => $mailbox->id, 'address' => $mailbox->address,
                'callback_url' => $poll->document['callback_url'], 'webhook_secret' => $poll->document['webhook_secret'],
                'cursor' => json_decode($checkpoint->cursor, true), 'lease_token' => $token];
        });
        if ($lease === null) { return null; }
        try {
            // Never hold the checkpoint/database lock across an external credential callback.
            $credentials = $this->connections->resolve($lease['connection_id'], 'imap', minTtlSeconds: 120);
            $upstream = $credentials === null ? null : $this->upstreams->active('imap', $credentials['upstream_id']);
            if ($credentials === null || $upstream === null) {
                $this->release($id, $lease['lease_token']);
                return null;
            }
            return [...$lease,
                'upstream_address' => $upstream['address'], 'upstream_server_name' => $upstream['server_name'],
                'upstream_network_scope' => $upstream['network_scope'] ?? 'public',
                'username' => $credentials['username'], 'secret' => $credentials['secret'],
                'mechanism' => $credentials['mechanism']];
        } catch (Throwable $error) {
            $this->release($id, $lease['lease_token']);
            throw $error;
        }
    }

    public function acknowledge(string $id, string $token, int $uidValidity, int $uid): bool
    {
        if ($uidValidity < 1 || $uid < 1) { return false; }
        return DB::transaction(function () use ($id, $token, $uidValidity, $uid): bool {
            $row = DB::table('resource_checkpoints')->where('resource_id', $id)->lockForUpdate()->first();
            if ($row === null || $row->lease_token !== $token || $row->lease_until <= now('UTC')) { return false; }
            $cursor = json_decode($row->cursor, true);
            if ($cursor['uid_validity'] === $uidValidity && $uid <= $cursor['last_uid']) { return false; }
            DB::table('resource_checkpoints')->where('resource_id', $id)->update([
                'cursor' => json_encode(['uid_validity' => $uidValidity, 'last_uid' => $uid], JSON_THROW_ON_ERROR),
                'updated_at' => now('UTC')]);
            return true;
        });
    }

    public function release(string $id, string $token): bool
    {
        return DB::table('resource_checkpoints')->where('resource_id', $id)->where('lease_token', $token)
            ->update(['lease_token' => null, 'lease_until' => null, 'updated_at' => now('UTC')]) === 1;
    }

    private function validCallback(string $url): bool
    {
        $parts = parse_url($url);
        return strlen($url) <= 2048 && is_array($parts) && ($parts['scheme'] ?? null) === 'https'
            && is_string($parts['host'] ?? null) && $parts['host'] !== ''
            && ! isset($parts['user']) && ! isset($parts['pass']) && ! isset($parts['fragment'])
            && ! isset($parts['query']) && ! isset($parts['port'])
            && filter_var($url, FILTER_VALIDATE_URL) !== false;
    }
}
