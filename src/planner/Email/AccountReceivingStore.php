<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Illuminate\Support\Carbon;
use InvalidArgumentException;
use stdClass;
use Webong\Gateway\Resources\PlannerResourceStore;

/** Account-scoped mail receiving on the shared encrypted Planner document store. */
final readonly class AccountReceivingStore
{
    private const DOMAIN = 'email.receiving-domain';
    private const ROUTE = 'email.receiving-route';
    private const SETTINGS = 'email.receiving-settings';

    public function __construct(private PlannerResourceStore $resources, private TxtResolver $txt,
        private EmailRegistry $legacyDomains, private EmailRuntimePolicyStore $runtimePolicy,
        private EmailVerificationProfile $verificationProfile) {}

    public function settings(): ?stdClass
    {
        return $this->resources->byKey(self::SETTINGS, 'default');
    }

    /** @param array<string, mixed> $input */
    public function configure(array $input): stdClass
    {
        if (array_diff(array_keys($input), ['mx_host', 'forwarding_domain', 'private_callbacks']) !== []
            || ! is_string($input['mx_host'] ?? null)) {
            throw new InvalidArgumentException('Invalid receiving settings.');
        }
        $host = strtolower(rtrim($input['mx_host'], '.'));
        if (preg_match('/^(?=.{4,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])$/D', $host) !== 1) {
            throw new InvalidArgumentException('Invalid MX hostname.');
        }
        $forwarding = $input['forwarding_domain'] ?? null;
        if ($forwarding !== null) {
            if (! is_string($forwarding)) { throw new InvalidArgumentException('Invalid forwarding domain.'); }
            $forwarding = strtolower(rtrim($forwarding, '.'));
            $this->legacyDomains->requireActiveDomain($forwarding);
        }
        $callbacks = $input['private_callbacks'] ?? [];
        if (! is_array($callbacks) || ! array_is_list($callbacks) || count($callbacks) > 16) {
            throw new InvalidArgumentException('Invalid private callback grants.');
        }
        foreach ($callbacks as $grant) {
            if (! is_array($grant) || count($grant) !== 2 || array_diff(array_keys($grant), ['url', 'cidrs']) !== []
                || ! is_string($grant['url']) || ! $this->validCallbackURL($grant['url'], 'private')
                || ! is_array($grant['cidrs']) || ! array_is_list($grant['cidrs'])
                || $grant['cidrs'] === [] || count($grant['cidrs']) > 8) {
                throw new InvalidArgumentException('Invalid private callback grant.');
            }
            foreach ($grant['cidrs'] as $cidr) {
                if (! is_string($cidr) || ! $this->validPrivateCIDR($cidr)) {
                    throw new InvalidArgumentException('Invalid private callback CIDR.');
                }
            }
        }
        $doc = ['mx_host' => $host, 'forwarding_domain' => $forwarding,
            'private_callbacks' => $callbacks];
        $current = $this->settings();
        return $current === null ? $this->resources->create(self::SETTINGS, 'default', $doc)
            : $this->resources->replace(self::SETTINGS, $current->id, 'default', $doc, true, $current->revision);
    }

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
        if (preg_match('/^(?=.{4,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])$/D', $name) !== 1
            || $this->legacyDomains->domainByName($name) !== null
            || $this->resources->byKey(self::DOMAIN, $name) !== null) {
            throw new InvalidArgumentException('The receiving domain is invalid or already managed.');
        }
        // Pending claims are account-scoped. Only a successful TXT check may
        // acquire the globally unique domain key used by SMTP routing.
        $token = null;
        $profile = $this->verificationProfile->current();
        foreach ($this->resources->all('email.sending-domain') as $senderDomain) {
            if (($senderDomain->document['account_id'] ?? null) === $account
                && ($senderDomain->document['name'] ?? null) === $name) {
                $token = $senderDomain->document['verification_token'];
                $profile = $this->verificationProfile->saved($senderDomain->document);
                break;
            }
        }
        return $this->resources->create(self::DOMAIN, 'pending:'.$account.':'.$name, [
            'account_id' => $account, 'name' => $name,
            'verification_token' => $token ?? bin2hex(random_bytes(24)),
            'verification_profile' => $profile,
            'verified_at' => null,
        ], false);
    }

    public function domainName(stdClass $domain): string
    {
        return $domain->document['name'] ?? $domain->key;
    }

    public function verifyDomain(stdClass $domain): stdClass
    {
        $name = $this->domainName($domain);
        $record = $this->verificationProfile->record($name, $domain->document['verification_token'], $domain->document);
        if (! $this->verificationProfile->hasRecord($record)) {
            throw new VerificationRecordMissing('The domain verification TXT record was not found.');
        }
        $owner = $this->resources->byKey(self::DOMAIN, $name);
        if ($owner !== null && $owner->id !== $domain->id) {
            throw new EmailRouteRejected('The receiving domain is already verified by another account.');
        }
        $doc = $domain->document;
        $doc['verified_at'] = now('UTC')->toIso8601String();
        return $this->resources->replace(self::DOMAIN, $domain->id, $name, $doc, true, $domain->revision);
    }

    public function deleteDomain(stdClass $domain): bool
    {
        foreach ($this->routes($domain->document['account_id']) as $route) {
            if (($route->document['domain_id'] ?? null) === $domain->id) {
                throw new InvalidArgumentException('Remove receiving routes before deleting the domain.');
            }
        }
        return $this->resources->delete(self::DOMAIN, $domain->id, $domain->revision);
    }

    /** @return list<stdClass> */
    public function routes(string $account): array
    {
        return array_values(array_filter($this->resources->all(self::ROUTE),
            static fn (stdClass $row): bool => ($row->document['account_id'] ?? null) === $account));
    }

    public function route(string $account, string $id): ?stdClass
    {
        $row = $this->resources->find(self::ROUTE, $id);
        return $row !== null && ($row->document['account_id'] ?? null) === $account ? $row : null;
    }

    public function activeAddress(string $address): ?stdClass
    {
        $route = $this->resources->byKey(self::ROUTE, strtolower($address));
        if ($route === null || ! $route->enabled) { return null; }
        if (($route->document['mode'] ?? '') === 'forward') {
            try { $this->legacyDomains->requireActiveDomain(substr($route->key, (int) strrpos($route->key, '@') + 1)); }
            catch (EmailRouteRejected) { return null; }
            return $route;
        }
        $domain = $this->resources->find(self::DOMAIN, $route->document['domain_id']);
        if ($domain === null || ! $domain->enabled || $domain->document['verified_at'] === null) { return null; }
        $ttl = $this->runtimePolicy->current()['verification_ttl_seconds'];
        if ($ttl === 0 || Carbon::parse($domain->document['verified_at'])->lte(now()->subSeconds($ttl))) {
            $this->verifyDomain($domain);
        }
        return $route;
    }

    public function reservedAddress(string $address): bool
    {
        return $this->resources->byKey(self::ROUTE, strtolower($address)) !== null;
    }

    public function privateCallbackCidrs(stdClass $route): ?string
    {
        if (($route->document['network_scope'] ?? 'public') !== 'private') { return null; }
        foreach ($this->settings()?->document['private_callbacks'] ?? [] as $grant) {
            if ($grant['url'] === $route->document['callback_url']) {
                return implode(',', $grant['cidrs']);
            }
        }
        throw new EmailRouteRejected('The private callback grant was revoked.');
    }

    public function managedDomain(string $address): bool
    {
        $at = strrpos($address, '@');
        if ($at === false) { return false; }
        $name = strtolower(substr($address, $at + 1));
        return $this->resources->byKey(self::DOMAIN, $name) !== null
            || $name === ($this->settings()?->document['forwarding_domain'] ?? null);
    }

    /** @return array{record: stdClass, secret: string} */
    public function createRoute(string $account, array $input): array
    {
        $mode = $input['mode'] ?? null;
        if (! in_array($mode, ['mx', 'forward', 'worker'], true)) { throw new InvalidArgumentException('Invalid receiving mode.'); }
        if ($mode !== 'worker' && ! is_string($this->settings()?->document['mx_host'] ?? null)) {
            throw new EmailRouteRejected('The Gateway receiving MX hostname is not configured.');
        }
        $scope = $input['network_scope'] ?? 'public';
        if (! in_array($scope, ['public', 'private'], true)) {
            throw new InvalidArgumentException('Invalid callback network scope.');
        }
        $providedSecret = $input['webhook_secret'] ?? null;
        if ($providedSecret !== null && (! is_string($providedSecret)
            || preg_match('/^[a-f0-9]{64}$/D', $providedSecret) !== 1)) {
            throw new InvalidArgumentException('Webhook secret must be 32 random bytes encoded as lowercase hex.');
        }
        if ($mode === 'mx' || $mode === 'worker') {
            $domain = $this->domain($account, (string) ($input['domain_id'] ?? ''));
            if ($domain === null || ! $domain->enabled || $domain->document['verified_at'] === null) {
                throw new EmailRouteRejected('The receiving domain is not verified.');
            }
            $local = strtolower((string) ($input['local_part'] ?? ''));
        } else {
            if (isset($input['domain_id']) || isset($input['local_part'])) {
                throw new InvalidArgumentException('Forwarding routes choose their own addresses.');
            }
            $externalId = $input['external_id'] ?? null;
            if (! is_string($externalId) || preg_match('/^[A-Za-z0-9._:-]{1,128}$/D', $externalId) !== 1
                || $providedSecret === null) {
                throw new InvalidArgumentException('Forwarding requires a stable external ID and client-generated secret.');
            }
            foreach ($this->routes($account) as $existing) {
                if (($existing->document['external_id'] ?? null) === $externalId) {
                    if ($existing->document['webhook_secret'] !== $providedSecret
                        || $existing->document['callback_url'] !== ($input['callback_url'] ?? null)
                        || ($existing->document['network_scope'] ?? 'public') !== $scope) {
                        throw new EmailRouteRejected('Forwarding route already exists with different details.');
                    }
                    return ['record' => $existing, 'secret' => $providedSecret];
                }
            }
            $name = $this->settings()?->document['forwarding_domain'] ?? null;
            if (! is_string($name) || $name === '') {
                throw new EmailRouteRejected('The forwarding domain is not configured.');
            }
            $legacy = $this->legacyDomains->requireActiveDomain($name);
            $domain = (object) ['id' => $legacy->id, 'key' => $legacy->domain];
            $key = (string) config('app.key');
            if ($key === '') { throw new InvalidArgumentException('Gateway application key is unavailable.'); }
            $local = 'z-'.substr(hash_hmac('sha256', $account."\n".$externalId, $key), 0, 32);
        }
        if (preg_match('/^[a-z0-9][a-z0-9._+-]{0,63}$/D', $local) !== 1
            || str_contains($local, '..') || str_ends_with($local, '.') || preg_match('/^srs[01]-/i', $local) === 1) {
            throw new InvalidArgumentException('Invalid receiving local part.');
        }
        $url = (string) ($input['callback_url'] ?? '');
        if (! $this->validCallbackURL($url, $scope)) {
            throw new InvalidArgumentException('A public HTTPS callback URL is required.');
        }
        if ($scope === 'private') {
            $approved = false;
            foreach ($this->settings()?->document['private_callbacks'] ?? [] as $grant) {
                if ($grant['url'] === $url) { $approved = true; break; }
            }
            if (! $approved) { throw new EmailRouteRejected('Private callback URL is not operator-approved.'); }
        }
        $secret = $providedSecret ?? bin2hex(random_bytes(32));
        $address = $local.'@'.$domain->key;
        if (($existing = $this->resources->byKey(self::ROUTE, $address)) !== null) {
            if (($existing->document['account_id'] ?? null) === $account
                && ($existing->document['mode'] ?? null) === $mode
                && ($existing->document['callback_url'] ?? null) === $url
                && ($existing->document['network_scope'] ?? 'public') === $scope
                && $providedSecret !== null && $existing->document['webhook_secret'] === $providedSecret) {
                return ['record' => $existing, 'secret' => $providedSecret];
            }
            throw new EmailRouteRejected('Receiving address already exists.');
        }
        $record = $this->resources->create(self::ROUTE, $address, [
            'account_id' => $account, 'mode' => $mode, 'domain_id' => $domain->id,
            'callback_url' => $url, 'webhook_secret' => $secret,
            'network_scope' => $scope,
            'external_id' => $input['external_id'] ?? null,
        ]);
        return ['record' => $record, 'secret' => $secret];
    }

    private function validCallbackURL(string $url, string $scope): bool
    {
        if (strlen($url) > 2048 || preg_match('/[\x00-\x20\x7f]/', $url) === 1) { return false; }
        $parts = parse_url($url);
        return is_array($parts) && ($parts['scheme'] ?? '') === 'https'
            && is_string($parts['host'] ?? null) && $parts['host'] !== ''
            && ! isset($parts['user']) && ! isset($parts['pass']) && ! isset($parts['fragment'])
            && ($scope === 'private' || ! isset($parts['port']) || $parts['port'] === 443);
    }

    private function validPrivateCIDR(string $cidr): bool
    {
        $parts = explode('/', $cidr);
        if (count($parts) !== 2 || filter_var($parts[0], FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) === false
            || ! ctype_digit($parts[1])) { return false; }
        $bits = (int) $parts[1];
        if ($bits < 8 || $bits > 32) { return false; }
        $address = ip2long($parts[0]);
        $mask = $bits === 32 ? 0xffffffff : (0xffffffff << (32 - $bits)) & 0xffffffff;
        if (($address & $mask) !== $address) { return false; }
        return ($bits >= 8 && ($address & 0xff000000) === 0x0a000000)
            || ($bits >= 12 && ($address & 0xfff00000) === 0xac100000)
            || ($bits >= 16 && ($address & 0xffff0000) === 0xc0a80000);
    }

    public function setRouteEnabled(stdClass $route, bool $enabled): stdClass
    {
        return $this->resources->replace(self::ROUTE, $route->id, $route->key, $route->document,
            $enabled, $route->revision);
    }

    public function switchRouteToWorker(stdClass $route): stdClass
    {
        if (($route->document['mode'] ?? null) !== 'mx') {
            throw new EmailRouteRejected('Only an MX receiving route can switch to Worker ingress.');
        }
        $account = $route->document['account_id'] ?? '';
        $domain = $this->domain($account, $route->document['domain_id'] ?? '');
        if ($domain === null || ! $domain->enabled || $domain->document['verified_at'] === null
            || ! str_ends_with($route->key, '@'.$this->domainName($domain))) {
            throw new EmailRouteRejected('The receiving domain is not verified and active.');
        }
        $document = $route->document;
        $document['mode'] = 'worker';
        return $this->resources->replace(self::ROUTE, $route->id, $route->key,
            $document, $route->enabled, $route->revision);
    }

    public function switchRouteToMx(stdClass $route): stdClass
    {
        if (($route->document['mode'] ?? null) !== 'worker') {
            throw new EmailRouteRejected('Only a Worker receiving route can switch to MX ingress.');
        }
        if (! is_string($this->settings()?->document['mx_host'] ?? null)) {
            throw new EmailRouteRejected('The Gateway receiving MX hostname is not configured.');
        }
        $account = $route->document['account_id'] ?? '';
        $domain = $this->domain($account, $route->document['domain_id'] ?? '');
        if ($domain === null || ! $domain->enabled || $domain->document['verified_at'] === null
            || ! str_ends_with($route->key, '@'.$this->domainName($domain))) {
            throw new EmailRouteRejected('The receiving domain is not verified and active.');
        }
        $document = $route->document;
        $document['mode'] = 'mx';
        return $this->resources->replace(self::ROUTE, $route->id, $route->key,
            $document, $route->enabled, $route->revision);
    }

    public function deleteRoute(stdClass $route): bool
    {
        return $this->resources->delete(self::ROUTE, $route->id, $route->revision);
    }
}
