<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\PathBinding;
use Webong\Proxy\WebProxyChannelManager;
use Webong\Proxy\WebProxyRegistryManager;

/** Projects the endpoint's CMS route kind onto an application path binding. */
final readonly class PlannerRouteKindPathResolver implements CacheablePathResolver
{
    public function __construct(
        private PathResolver $paths,
        private WebProxyRegistryManager $registries,
        private WebProxyChannelManager $channels,
    ) {}

    public function resolve(IngressRequest $request): ?PathBinding
    {
        $binding = $this->paths->resolve($request);
        if ($binding === null || $request->protocol !== 'http') {
            return $binding;
        }
        $resolved = $this->registries->resolveByKey(
            $binding->endpointKey,
            $this->channels->registries($binding->channel),
        );
        if ($resolved === null) {
            return $binding;
        }
        $kind = EndpointRouteKind::fromMetadata($resolved->endpoint->record->metadata);
        return new PathBinding(
            endpointKey: $binding->endpointKey, scope: $binding->scope,
            key: $binding->key, channel: $binding->channel,
            destinationMetadata: $binding->destinationMetadata, kind: $kind,
        );
    }

    public function cacheKey(IngressRequest $request): string
    {
        if (! $this->paths instanceof CacheablePathResolver) { return ''; }
        $key = $this->paths->cacheKey($request);
        return $key === '' ? '' : $request->protocol.'|'.$key;
    }
}
