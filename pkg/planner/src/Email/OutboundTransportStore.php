<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Database\QueryException;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Operator-owned outbound routing. Credential documents are separate from public transport resources. */
final readonly class OutboundTransportStore
{
    private const TRANSPORT = 'email.outbound-transport';
    private const SECRET = 'email.outbound-credential';
    private const POOL = 'email.outbound-pool';
    private const BINDING = 'email.outbound-binding';
    private const ATTEMPT = 'email.outbound-attempt';

    public function __construct(private PlannerResourceStore $resources, private ManagedSendingStore $sending) {}

    /** @return list<stdClass> */
    public function transports(): array { return $this->resources->all(self::TRANSPORT); }
    public function transport(string $id): ?stdClass { return $this->resources->find(self::TRANSPORT, $id); }
    /** @return list<stdClass> */
    public function pools(): array { return $this->resources->all(self::POOL); }
    public function pool(string $id): ?stdClass { return $this->resources->find(self::POOL, $id); }
    /** @return list<stdClass> */
    public function bindings(): array { return $this->resources->all(self::BINDING); }
    public function binding(string $id): ?stdClass { return $this->resources->find(self::BINDING, $id); }
    /** @return list<stdClass> */
    public function attempts(): array { return $this->resources->all(self::ATTEMPT); }
    public function attempt(string $id): ?stdClass { return $this->resources->byKey(self::ATTEMPT, $id); }

    public function createTransport(array $input): stdClass
    {
        if (array_diff(array_keys($input), ['name', 'type', 'address', 'server_name', 'local_name', 'tls_mode',
            'username', 'password', 'authorized_domains', 'domain_readiness', 'account_id', 'healthy', 'enabled', 'rate_limit_per_minute']) !== []
            || ! is_string($input['name'] ?? null) || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $input['name']) !== 1
            || ($input['type'] ?? null) !== 'smtp'
            || ! is_string($input['address'] ?? null) || ! $this->validAddress($input['address'])
            || ! in_array($input['tls_mode'] ?? null, ['starttls', 'implicit'], true)
            || ! is_string($input['username'] ?? null) || $input['username'] === '' || strlen($input['username']) > 320
            || ! is_string($input['password'] ?? null) || $input['password'] === '' || strlen($input['password']) > 8192
            || ! is_array($input['authorized_domains'] ?? null) || ! array_is_list($input['authorized_domains'])
            || $input['authorized_domains'] === [] || count($input['authorized_domains']) > 100
            || (isset($input['account_id']) && (! is_string($input['account_id']) || $input['account_id'] === ''))
            || (isset($input['healthy']) && ! is_bool($input['healthy']))
            || (isset($input['rate_limit_per_minute']) && (! is_int($input['rate_limit_per_minute'])
                || $input['rate_limit_per_minute'] < 1 || $input['rate_limit_per_minute'] > 1_000_000))
            || (isset($input['enabled']) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid outbound transport.');
        }
        foreach ($input['authorized_domains'] as $domain) {
            if (! is_string($domain) || ! $this->validDomain($domain)) {
                throw new InvalidArgumentException('Invalid outbound sender domain.');
            }
        }
        $readiness = $input['domain_readiness'] ?? [];
        if (! is_array($readiness) || ($readiness !== [] && array_is_list($readiness))
            || array_diff(array_keys($readiness), $input['authorized_domains']) !== []) {
            throw new InvalidArgumentException('Invalid sender readiness.');
        }
        foreach ($readiness as $domain => $status) {
            $this->validateReadiness($status);
        }
        foreach (['server_name', 'local_name'] as $field) {
            if (isset($input[$field]) && (! is_string($input[$field]) || ! $this->validDomain($input[$field]))) {
                throw new InvalidArgumentException('Invalid outbound SMTP hostname.');
            }
        }
        $secret = $this->resources->create(self::SECRET, $input['name'], ['password' => $input['password']]);
        try {
            return $this->resources->create(self::TRANSPORT, $input['name'], [
                'name' => $input['name'], 'type' => 'smtp', 'address' => $input['address'],
                'server_name' => $input['server_name'] ?? null, 'local_name' => $input['local_name'] ?? null,
                'tls_mode' => $input['tls_mode'], 'username' => $input['username'],
                'credential_ref' => $secret->id, 'authorized_domains' => array_values(array_unique($input['authorized_domains'])),
                'account_id' => $input['account_id'] ?? null, 'healthy' => $input['healthy'] ?? false,
                'rate_limit_per_minute' => $input['rate_limit_per_minute'] ?? null,
                'domain_readiness' => $readiness,
            ], $input['enabled'] ?? true);
        } catch (\Throwable $exception) {
            $this->resources->delete(self::SECRET, $secret->id, $secret->revision);
            throw $exception;
        }
    }

    public function setHealth(stdClass $transport, bool $healthy): stdClass
    {
        $doc = $transport->document;
        $doc['healthy'] = $healthy;
        return $this->resources->replace(self::TRANSPORT, $transport->id, $transport->key,
            $doc, $transport->enabled, $transport->revision);
    }

    public function setEnabled(stdClass $transport, bool $enabled): stdClass
    {
        return $this->resources->replace(self::TRANSPORT, $transport->id, $transport->key,
            $transport->document, $enabled, $transport->revision);
    }

    public function setDomainReadiness(stdClass $transport, string $domain, array $status): stdClass
    {
        if (! in_array($domain, $transport->document['authorized_domains'], true)) {
            throw new InvalidArgumentException('Sender domain is not authorized.');
        }
        $this->validateReadiness($status);
        $doc = $transport->document;
        $doc['domain_readiness'][$domain] = $status;
        return $this->resources->replace(self::TRANSPORT, $transport->id, $transport->key,
            $doc, $transport->enabled, $transport->revision);
    }

    public function rotateCredential(stdClass $transport, string $password): void
    {
        if ($password === '' || strlen($password) > 8192) { throw new InvalidArgumentException('Invalid SMTP credential.'); }
        $secret = $this->resources->find(self::SECRET, $transport->document['credential_ref']);
        if ($secret === null) { throw new InvalidArgumentException('SMTP credential is unavailable.'); }
        $this->resources->replace(self::SECRET, $secret->id, $secret->key,
            ['password' => $password], true, $secret->revision);
    }

    public function createPool(array $input): stdClass
    {
        if (array_diff(array_keys($input), ['name', 'members', 'enabled']) !== []
            || ! is_string($input['name'] ?? null) || preg_match('/^[a-z][a-z0-9._-]{0,63}$/D', $input['name']) !== 1
            || ! is_array($input['members'] ?? null) || ! array_is_list($input['members'])
            || $input['members'] === [] || count($input['members']) > 32
            || (isset($input['enabled']) && ! is_bool($input['enabled']))) {
            throw new InvalidArgumentException('Invalid outbound pool.');
        }
        $seen = [];
        foreach ($input['members'] as $member) {
            if (! is_array($member) || array_diff(array_keys($member), ['transport_id', 'priority', 'weight']) !== []
                || ! is_string($member['transport_id'] ?? null) || isset($seen[$member['transport_id']])
                || $this->transport($member['transport_id']) === null
                || ! is_int($member['priority'] ?? null) || $member['priority'] < 0 || $member['priority'] > 100
                || ! is_int($member['weight'] ?? null) || $member['weight'] < 1 || $member['weight'] > 1000) {
                throw new InvalidArgumentException('Invalid outbound pool member.');
            }
            $seen[$member['transport_id']] = true;
        }
        return $this->resources->create(self::POOL, $input['name'], [
            'name' => $input['name'], 'members' => $input['members'],
        ], $input['enabled'] ?? true);
    }

    public function setPoolEnabled(stdClass $pool, bool $enabled): stdClass
    {
        return $this->resources->replace(self::POOL, $pool->id, $pool->key,
            $pool->document, $enabled, $pool->revision);
    }

    public function setBindingEnabled(stdClass $binding, bool $enabled): stdClass
    {
        return $this->resources->replace(self::BINDING, $binding->id, $binding->key,
            $binding->document, $enabled, $binding->revision);
    }

    public function bind(array $input): stdClass
    {
        if (array_diff(array_keys($input), ['account_id', 'scope', 'sender_id', 'pool_id']) !== []
            || ! is_string($input['account_id'] ?? null) || $input['account_id'] === ''
            || ! in_array($input['scope'] ?? null, ['shared_route', 'domain'], true)
            || ! is_string($input['sender_id'] ?? null) || ! is_string($input['pool_id'] ?? null)
            || ($pool = $this->pool($input['pool_id'])) === null || ! $pool->enabled) {
            throw new InvalidArgumentException('Invalid outbound binding.');
        }
        $account = $input['account_id'];
        if ($input['scope'] === 'shared_route') {
            $route = $this->sending->route($account, $input['sender_id']);
            if ($route === null || ($route->document['mode'] ?? null) !== 'shared') {
                throw new InvalidArgumentException('Shared route does not belong to account.');
            }
            $address = $this->sending->sharedFromAddress();
            $domain = substr($address, (int) strrpos($address, '@') + 1);
        } else {
            $row = $this->sending->domain($account, $input['sender_id']);
            if ($row === null || ! $row->enabled || ($row->document['verified_at'] ?? null) === null
                || ! $this->sending->dnsReady($row)) {
                throw new InvalidArgumentException('Sending domain is not verified for account.');
            }
            $domain = $row->document['name'];
        }
        foreach ($pool->document['members'] as $member) {
            $transport = $this->transport($member['transport_id']);
            if ($transport === null || ! $this->domainReady($transport, $domain)
                || ($transport->document['account_id'] ?? null) !== null
                && $transport->document['account_id'] !== $account) {
                throw new InvalidArgumentException('Every pool transport must authorize the sender domain and account.');
            }
        }
        $key = $account.':'.$input['scope'].':'.$input['sender_id'];
        return $this->resources->create(self::BINDING, $key, [
            'account_id' => $account, 'scope' => $input['scope'], 'sender_id' => $input['sender_id'],
            'pool_id' => $pool->id, 'sender_domain' => $domain,
        ]);
    }

    public function boundPool(string $account, string $routeID, string $domainID): ?stdClass
    {
        $binding = $this->resources->byKey(self::BINDING, $account.':shared_route:'.$routeID);
        if ($binding === null && $domainID !== '') {
            $binding = $this->resources->byKey(self::BINDING, $account.':domain:'.$domainID);
        }
        $pool = $binding !== null && $binding->enabled ? $this->pool($binding->document['pool_id']) : null;
        return $pool !== null && $pool->enabled ? $pool : null;
    }

    /** @return array{pool_id: string, account_id: string, route_id: string, domain_id: string, sender_domain: string}|null */
    public function senderBinding(string $account, string $routeID): ?array
    {
        $route = $this->sending->route($account, $routeID);
        if ($route === null || ! $route->enabled) { return null; }
        $domainID = ($route->document['mode'] ?? null) === 'shared' ? '' : ($route->document['domain_id'] ?? '');
        if ($domainID !== '') {
            $sendingDomain = $this->sending->domain($account, $domainID);
            if ($sendingDomain === null || ! $sendingDomain->enabled
                || ($sendingDomain->document['verified_at'] ?? null) === null
                || ! $this->sending->dnsReady($sendingDomain)) { return null; }
        }
        $pool = $this->boundPool($account, $routeID, $domainID);
        if ($pool === null) { return null; }
        $shared = $domainID === '' ? $this->sending->sharedFromAddress() : null;
        $domain = $domainID === '' ? (is_string($shared) && str_contains($shared, '@')
            ? substr($shared, (int) strrpos($shared, '@') + 1) : null)
            : $this->sending->domain($account, $domainID)?->document['name'];
        return is_string($domain) ? ['pool_id' => $pool->id, 'account_id' => $account,
            'route_id' => $routeID, 'domain_id' => $domainID, 'sender_domain' => $domain] : null;
    }

    /** @return list<stdClass> Ordered, eligible transports for one logical delivery. */
    public function ordered(string $poolID, string $account, string $senderDomain, string $deliveryID): array
    {
        $pool = $this->pool($poolID);
        if ($pool === null || ! $pool->enabled) { return []; }
        $groups = [];
        foreach ($pool->document['members'] as $member) {
            $transport = $this->transport($member['transport_id']);
            if ($transport === null || ! $transport->enabled || ! ($transport->document['healthy'] ?? false)
                || ! $this->domainReady($transport, $senderDomain)
                || (($transport->document['account_id'] ?? null) !== null && $transport->document['account_id'] !== $account)) {
                continue;
            }
            $groups[$member['priority']][] = ['transport' => $transport, 'weight' => $member['weight']];
        }
        ksort($groups, SORT_NUMERIC);
        $ordered = [];
        foreach ($groups as $priority => $members) {
            usort($members, static fn (array $a, array $b): int => strcmp($a['transport']->id, $b['transport']->id));
            while ($members !== []) {
                $total = array_sum(array_column($members, 'weight'));
                $hash = hash('sha256', $deliveryID.':'.$priority.':'.count($ordered));
                $slot = hexdec(substr($hash, 0, 12)) % $total;
                foreach ($members as $index => $member) {
                    $slot -= $member['weight'];
                    if ($slot < 0) {
                        $ordered[] = $member['transport'];
                        array_splice($members, $index, 1);
                        break;
                    }
                }
            }
        }
        return $ordered;
    }

    /** Pin the ordered transport IDs in the shared encrypted control plane before the first send. */
    public function planAttempt(string $poolID, string $account, string $senderDomain, string $deliveryID,
        string $routeID, string $domainID): array
    {
        if (preg_match('/^[a-f0-9]{32,64}$/D', $deliveryID) !== 1 || $account === '' || ! $this->validDomain($senderDomain)) {
            throw new InvalidArgumentException('Invalid outbound attempt.');
        }
        $binding = $this->senderBinding($account, $routeID);
        if ($binding === null || $binding['pool_id'] !== $poolID
            || $binding['domain_id'] !== $domainID || $binding['sender_domain'] !== $senderDomain) {
            throw new EmailRouteRejected('Outbound pool is not bound to this sender.');
        }
        $existing = $this->attempt($deliveryID);
        if ($existing === null) {
            $ordered = $this->ordered($poolID, $account, $senderDomain, $deliveryID);
            if ($ordered === []) { throw new EmailRouteRejected('No authorized outbound transport is available.'); }
            try {
                $existing = $this->resources->create(self::ATTEMPT, $deliveryID, [
                    'account_id' => $account, 'pool_id' => $poolID, 'sender_domain' => $senderDomain,
                    'transport_ids' => array_map(static fn (stdClass $row): string => $row->id, $ordered),
                    'state' => 'queued', 'selected_transport_id' => $ordered[0]->id,
                ]);
            } catch (QueryException) {
                $existing = $this->attempt($deliveryID);
            }
        }
        if ($existing === null || $existing->document['account_id'] !== $account
            || $existing->document['pool_id'] !== $poolID || $existing->document['sender_domain'] !== $senderDomain) {
            throw new EmailRouteRejected('Outbound delivery identity belongs to another sender.');
        }
        return array_values(array_filter(array_map($this->transport(...), $existing->document['transport_ids']),
            fn (?stdClass $row): bool => $row !== null && $row->enabled
                && ($row->document['healthy'] ?? false) && $this->domainReady($row, $senderDomain)
                && (($row->document['account_id'] ?? null) === null || $row->document['account_id'] === $account)));
    }

    public function recordAttempt(string $deliveryID, string $transportID, string $state): ?stdClass
    {
        if (! in_array($state, ['attempted', 'accepted', 'temporarily_delayed', 'delivered', 'bounced', 'rejected', 'complained', 'ambiguous'], true)) {
            throw new InvalidArgumentException('Invalid outbound state.');
        }
        $attempt = $this->attempt($deliveryID);
        if ($attempt === null || ! in_array($transportID, $attempt->document['transport_ids'], true)) { return null; }
        $doc = $attempt->document;
        $doc['selected_transport_id'] = $transportID;
        $doc['state'] = $state;
        return $this->resources->replace(self::ATTEMPT, $attempt->id, $attempt->key,
            $doc, true, $attempt->revision);
    }

    /** Atomic across Gateway replicas sharing Planner's database. The key makes a retried reservation idempotent. */
    public function reserveCapacity(string $deliveryID, int $attemptIndex, string $transportID): bool
    {
        $attempt = $this->attempt($deliveryID);
        $transport = $this->transport($transportID);
        if ($attempt === null || $transport === null || ! $transport->enabled || ! ($transport->document['healthy'] ?? false)
            || ! in_array($transportID, $attempt->document['transport_ids'], true)
            || $attemptIndex < 0 || $attemptIndex > 1_000_000) {
            return false;
        }
        $limit = $transport->document['rate_limit_per_minute'] ?? null;
        if ($limit === null) { return true; }
        $minute = intdiv(time(), 60);
        try { return DB::transaction(function () use ($deliveryID, $attemptIndex, $transportID, $minute, $limit): bool {
            $key = ['delivery_id' => $deliveryID, 'attempt_index' => $attemptIndex, 'transport_id' => $transportID];
            if (DB::table('email_outbound_rate_reservations')->where($key)->exists()) { return true; }
            DB::table('email_outbound_rate_usage')->insertOrIgnore([
                'transport_id' => $transportID, 'minute' => $minute, 'attempts' => 0,
            ]);
            $updated = DB::table('email_outbound_rate_usage')->where('transport_id', $transportID)
                ->where('minute', $minute)->where('attempts', '<', $limit)->increment('attempts');
            if ($updated !== 1) { return false; }
            DB::table('email_outbound_rate_reservations')->insert([...$key, 'minute' => $minute]);
            return true;
        }); } catch (QueryException) {
            return DB::table('email_outbound_rate_reservations')->where([
                'delivery_id' => $deliveryID, 'attempt_index' => $attemptIndex, 'transport_id' => $transportID,
            ])->exists();
        }
    }

    public function pruneUsage(int $days): int
    {
        if ($days < 1) { throw new InvalidArgumentException('Invalid outbound retention.'); }
        $before = intdiv(time() - $days * 86400, 60);
        return DB::transaction(function () use ($before): int {
            $reservations = DB::table('email_outbound_rate_reservations')->where('minute', '<', $before)->delete();
            $usage = DB::table('email_outbound_rate_usage')->where('minute', '<', $before)->delete();
            return $reservations + $usage;
        });
    }

    public function credential(stdClass $transport): ?string
    {
        return $this->resources->find(self::SECRET, $transport->document['credential_ref'])?->document['password'] ?? null;
    }

    private function validDomain(string $domain): bool
    {
        return preg_match('/^(?=.{4,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])$/D', $domain) === 1;
    }

    private function validAddress(string $address): bool
    {
        $parts = explode(':', $address);
        return count($parts) === 2 && $this->validDomain($parts[0]) && ctype_digit($parts[1])
            && (int) $parts[1] >= 1 && (int) $parts[1] <= 65535;
    }

    private function validateReadiness(mixed $status): void
    {
        if (! is_array($status) || count($status) !== 2 || array_diff(array_keys($status), ['spf', 'dkim']) !== []
            || ! in_array($status['spf'] ?? null, ['ready', 'pending', 'not_required'], true)
            || ! in_array($status['dkim'] ?? null, ['ready', 'pending', 'not_required'], true)) {
            throw new InvalidArgumentException('Invalid provider-neutral sender readiness.');
        }
    }

    private function domainReady(stdClass $transport, string $domain): bool
    {
        if (! in_array($domain, $transport->document['authorized_domains'], true)) { return false; }
        $status = $transport->document['domain_readiness'][$domain] ?? [];
        return in_array($status['spf'] ?? null, ['ready', 'not_required'], true)
            && in_array($status['dkim'] ?? null, ['ready', 'not_required'], true);
    }
}
