<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\PathBinding;
use Webong\WebProxy\Models\WebProxyEndpoint;

class DnsHookPathResolver implements PathResolver
{
    public function __construct(protected readonly ?PathResolver $fallback = null) {}

    public function resolve(IngressRequest $request): ?PathBinding
    {
        if ($request->protocol !== 'dns') {
            return $this->fallback?->resolve($request);
        }

        $token = strtolower((string) ($request->attributes['endpoint'] ?? ltrim($request->path, '/')));
        if ($token === '') {
            return null;
        }

        $hook = WebProxyEndpoint::query()
            ->where('client', trim((string) config('gateway.dns.client', 'gateway')))
            ->where('endpoint_key', 'dns-'.$token)
            ->where('is_active', true)
            ->where('metadata->_gateway->kind', 'dns_hook')
            ->first();
        if ($hook === null) {
            return null;
        }

        return new PathBinding(
            endpointKey: $hook->endpoint_key,
            scope: 'dns',
            key: 'query',
        );
    }
}
