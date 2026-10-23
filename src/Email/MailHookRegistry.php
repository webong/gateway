<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use RuntimeException;
use Webong\Gateway\RegistryRouteCache;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EndpointRegistry;
use Webong\Proxy\Models\WebProxyEndpoint;

/** Verified domains may expose a catch-all hook and exact-address hooks. */
final readonly class MailHookRegistry
{
    public function __construct(
        private EmailRegistry $domains,
        private EndpointRegistry $endpoints,
        private RegistryRouteCache $routeCache,
    ) {}

    public function ensure(string $domain, ?string $name = null, ?string $registry = null, string $localPart = '*'): WebProxyEndpoint
    {
        $domain = strtolower(rtrim($domain, '.'));
        $this->domains->requireActiveDomain($domain);
        $client = trim((string) config('gateway.email.hook_client', 'gateway'));
        if ($client === '') { throw new RuntimeException('Mail hook client is not configured.'); }
        $definition = new EndpointDefinition(
            client: $client, externalId: 'mail-hook:'.$domain.($localPart === '*' ? '' : ':'.$localPart),
            signingSecret: null, verificationToken: null,
            endpointKey: 'mail-hook-'.substr(hash('sha256', $domain.($localPart === '*' ? '' : ':'.$localPart)), 0, 32),
            credentialOwnerId: null, managed: true, registry: $registry,
            metadata: ['_gateway' => ['kind' => 'mail_hook', 'protocol' => 'smtp',
                'domain' => $domain, 'local_part' => $localPart, 'name' => $name,
                'routing' => ['scope' => 'smtp', 'key' => 'recipient']]],
        );
        $endpoint = $this->endpoints->ensure($definition);
        $model = WebProxyEndpoint::query()->findOrFail($endpoint->record->id);
        if (! $model->is_active) { $model->update(['is_active' => true]); }
        $this->routeCache->invalidatePaths();

        return $model->refresh();
    }

    public function activeForAddress(string $address): ?WebProxyEndpoint
    {
        $at = strrpos($address, '@');
        if ($at === false) { return null; }
        $localPart = strtolower(substr($address, 0, $at));
        $domain = strtolower(rtrim(substr($address, $at + 1), '.'));
        $hooks = WebProxyEndpoint::query()
            ->where('client', trim((string) config('gateway.email.hook_client', 'gateway')))
            ->where('metadata->_gateway->kind', 'mail_hook')
            ->where('metadata->_gateway->domain', $domain)
            ->where('is_active', true)->get();
        $wildcard = null;
        foreach ($hooks as $hook) {
            $part = $hook->metadata['_gateway']['local_part'] ?? '*';
            if ($part === $localPart) { return $hook; }
            if ($part === '*') { $wildcard = $hook; }
        }

        return $wildcard !== null && ! $this->domains->hasAliasMatch($address) ? $wildcard : null;
    }

    public function find(string $id): ?WebProxyEndpoint
    {
        return WebProxyEndpoint::query()
            ->where('client', trim((string) config('gateway.email.hook_client', 'gateway')))
            ->where('metadata->_gateway->kind', 'mail_hook')
            ->whereKey($id)->first();
    }

    /** @return list<WebProxyEndpoint> */
    public function all(): array
    {
        return WebProxyEndpoint::query()
            ->where('client', trim((string) config('gateway.email.hook_client', 'gateway')))
            ->where('metadata->_gateway->kind', 'mail_hook')->orderBy('created_at')->get()->all();
    }

    public function setActive(WebProxyEndpoint $hook, bool $active): WebProxyEndpoint
    {
        if ($active) { $this->domains->requireActiveDomain($hook->metadata['_gateway']['domain']); }
        $hook->update(['is_active' => $active]);
        $this->routeCache->invalidatePaths();

        return $hook->refresh();
    }

    public function delete(WebProxyEndpoint $hook): void
    {
        $this->setActive($hook, false);
    }
}
