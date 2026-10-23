<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Carbon;
use Illuminate\Support\Facades\DB;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Accounts\AccountStore;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Gateway-owned sender identities; upstream transport is an operator concern. */
final readonly class ManagedSendingStore
{
    private const DOMAIN = 'email.sending-domain';
    private const ROUTE = 'email.sending-route';

    public function __construct(private PlannerResourceStore $resources, private AccountStore $accounts,
        private TxtResolver $txt, private EmailRuntimePolicyStore $policy,
        private EmailVerificationProfile $verificationProfile, private EmailDkimKeyStore $dkimKeys) {}

    /** @return list<stdClass> */
    public function domains(string $account): array
    {
        return array_values(array_filter($this->resources->all(self::DOMAIN),
            static fn (stdClass $row): bool => ($row->document['account_id'] ?? null) === $account));
    }

    public function domain(string $account, string $id): ?stdClass
    {
        $row = $this->resources->find(self::DOMAIN, $id);
        return $row !== null && ($row->document['account_id'] ?? null) === $account ? $row : null;
    }

    public function createDomain(string $account, string $name): stdClass
    {
        $name = strtolower(rtrim(trim($name), '.'));
        if (! $this->accounts->exists($account) || ! $this->validDomain($name)
            || $this->resources->byKey(self::DOMAIN, $name) !== null) {
            throw new InvalidArgumentException('Invalid or already managed sending domain.');
        }
        foreach ($this->domains($account) as $existing) {
            if (($existing->document['name'] ?? null) === $name) {
                throw new InvalidArgumentException('Sending domain already exists.');
            }
        }
        $token = null;
        $profile = $this->verificationProfile->current();
        foreach ($this->resources->all('email.receiving-domain') as $receivingDomain) {
            if (($receivingDomain->document['account_id'] ?? null) === $account
                && ($receivingDomain->document['name'] ?? null) === $name) {
                $token = $receivingDomain->document['verification_token'];
                $profile = $this->verificationProfile->saved($receivingDomain->document);
                break;
            }
        }
        return $this->resources->create(self::DOMAIN, 'pending:'.$account.':'.$name, [
            'account_id' => $account, 'name' => $name,
            'verification_token' => $token ?? bin2hex(random_bytes(24)),
            'verification_profile' => $profile, 'verified_at' => null,
        ], false);
    }

    public function verifyDomain(stdClass $domain): stdClass
    {
        $name = $domain->document['name'];
        $record = $this->verificationProfile->record($name, $domain->document['verification_token'], $domain->document);
        if (! $this->verificationProfile->hasRecord($record)) {
            throw new VerificationRecordMissing('The sending domain verification TXT record was not found.');
        }
        $owner = $this->resources->byKey(self::DOMAIN, $name);
        if ($owner !== null && $owner->id !== $domain->id) {
            throw new EmailRouteRejected('The sending domain belongs to another account.');
        }
        $document = $domain->document;
        $document['verified_at'] = now('UTC')->toIso8601String();
        return DB::transaction(function () use ($domain, $name, $document): stdClass {
            $verified = $this->resources->replace(self::DOMAIN, $domain->id, $name, $document, true, $domain->revision);
            $this->dkimKeys->ensureGenerated($name, (string) config('gateway.email.managed_dkim_selector'));
            return $verified;
        });
    }

    public function deleteDomain(stdClass $domain): void
    {
        foreach ($this->routes($domain->document['account_id']) as $route) {
            if (($route->document['domain_id'] ?? null) === $domain->id) {
                throw new EmailRouteRejected('Remove sending routes before deleting the domain.');
            }
        }
        $this->resources->delete(self::DOMAIN, $domain->id, $domain->revision);
    }

    /** @return list<stdClass> */
    public function routes(string $account): array
    {
        return $this->resources->allOwned(self::ROUTE, $account);
    }

    public function route(string $account, string $id): ?stdClass
    {
        return $this->resources->findOwned(self::ROUTE, $id, $account);
    }

    /** @return array{record: stdClass, password: string} */
    public function createRoute(string $account, array $input): array
    {
        if (($input['mode'] ?? null) === 'shared') {
            if (array_keys($input) !== ['mode'] || ! $this->accounts->exists($account)
                || ! $this->sharedFromAddress()) {
                throw new InvalidArgumentException('Shared sender is not configured.');
            }
            $password = bin2hex(random_bytes(32));
            $record = $this->resources->create(self::ROUTE, 'shared:'.$account, [
                'mode' => 'shared', 'password_hash' => password_hash($password, PASSWORD_DEFAULT),
            ], true, owner: $account);
            return ['record' => $record, 'password' => $password];
        }
        if (array_diff(array_keys($input), ['domain_id', 'local_part']) !== []
            || ! is_string($input['domain_id'] ?? null) || ! is_string($input['local_part'] ?? null)) {
            throw new InvalidArgumentException('Invalid managed sending route.');
        }
        $domain = $this->domain($account, $input['domain_id']);
        if ($domain === null || ! $domain->enabled || ($domain->document['verified_at'] ?? null) === null) {
            throw new EmailRouteRejected('Verify the sending domain first.');
        }
        if (! $this->dnsReady($domain)) {
            throw new EmailRouteRejected('Publish the managed sender DNS records and enable DKIM signing first.');
        }
        $local = strtolower($input['local_part']);
        if (preg_match('/^[a-z0-9][a-z0-9._+-]{0,63}$/D', $local) !== 1
            || str_contains($local, '..') || str_ends_with($local, '.')) {
            throw new InvalidArgumentException('Invalid sender local part.');
        }
        $password = bin2hex(random_bytes(32));
        $record = $this->resources->create(self::ROUTE, $local.'@'.$domain->document['name'], [
            'domain_id' => $domain->id, 'password_hash' => password_hash($password, PASSWORD_DEFAULT),
        ], true, owner: $account);
        return ['record' => $record, 'password' => $password];
    }

    public function authenticate(string $id, string $password): ?stdClass
    {
        $route = $this->resources->findAny(self::ROUTE, $id);
        if ($route === null || ! $route->enabled || ! password_verify($password, $route->document['password_hash'])) {
            return null;
        }
        if (($route->document['mode'] ?? null) === 'shared') {
            return $this->sharedFromAddress() !== null ? $route : null;
        }
        $domain = $this->domain($route->owner, $route->document['domain_id']);
        if ($domain === null || ! $domain->enabled || ($domain->document['verified_at'] ?? null) === null) {
            return null;
        }
        if (! $this->dnsReady($domain)) { return null; }
        $ttl = $this->policy->current()['verification_ttl_seconds'];
        if ($ttl === 0 || Carbon::parse($domain->document['verified_at'])->lte(now()->subSeconds($ttl))) {
            try { $this->verifyDomain($domain); }
            catch (VerificationRecordMissing|EmailRouteRejected) { return null; }
        }
        return $route;
    }

    public function setEnabled(stdClass $route, bool $enabled): stdClass
    {
        if ($enabled && ($route->document['mode'] ?? null) !== 'shared') {
            $domain = $this->domain($route->owner, $route->document['domain_id']);
            if ($domain === null || ! $domain->enabled || ! $this->dnsReady($domain)) {
                throw new EmailRouteRejected('Managed sender DNS is not ready.');
            }
        }
        return $this->resources->replace(self::ROUTE, $route->id, $route->key,
            $route->document, $enabled, $route->revision, $route->owner);
    }

    public function rotate(stdClass $route): array
    {
        $password = bin2hex(random_bytes(32));
        $document = $route->document;
        $document['password_hash'] = password_hash($password, PASSWORD_DEFAULT);
        $updated = $this->resources->replace(self::ROUTE, $route->id, $route->key,
            $document, $route->enabled, $route->revision, $route->owner);
        return ['record' => $updated, 'password' => $password];
    }

    public function deleteRoute(stdClass $route): void
    {
        $this->resources->delete(self::ROUTE, $route->id, $route->revision, $route->owner);
    }

    private function validDomain(string $name): bool
    {
        return preg_match('/^(?=.{4,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])$/D', $name) === 1;
    }

    public function sharedFromAddress(): ?string
    {
        $address = config('gateway.email.shared_from_address');
        return is_string($address) && strlen($address) <= 254
            && filter_var($address, FILTER_VALIDATE_EMAIL) !== false
            && $address === strtolower($address) ? $address : null;
    }

    /** @return array{dkim: array|null, spf: array|null} */
    public function dnsRecords(stdClass $domain): array
    {
        $name = $domain->document['name'];
        $spf = config('gateway.email.managed_spf_value');
        return ['dkim' => $this->dkimKeys->publicRecord($name),
            'spf' => is_string($spf) && $spf !== ''
                ? ['name' => $name, 'type' => 'TXT', 'value' => $spf] : null];
    }

    public function dnsReady(stdClass $domain): bool
    {
        if (config('gateway.email.managed_dkim_signing_enabled') !== true) { return false; }
        $records = $this->dnsRecords($domain);
        return $records['dkim'] !== null && $this->recordReady($records['dkim'])
            && ($records['spf'] === null || $this->recordReady($records['spf']));
    }

    public function recordReady(array $record): bool
    {
        return in_array($record['value'], array_map('trim', $this->txt->records($record['name'])), true);
    }
}
