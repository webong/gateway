<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Dns\Models\DnsHook;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\PathBinding;

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

        $hook = DnsHook::query()->active()->where('token', $token)->first();
        if ($hook === null) {
            return null;
        }

        return new PathBinding(
            endpointKey: (string) $hook->endpoint_key,
            scope: 'dns',
            key: 'query',
        );
    }
}
