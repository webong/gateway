<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Closure;
use Illuminate\Contracts\Cache\Repository;
use Throwable;
use Webong\WebProxy\DatabaseEndpointProvider;
use Webong\Gateway\Contracts\CacheablePathResolver;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\PathBinding;

final class RegistryRouteCache
{
    public function __construct(
        private readonly ?Repository $cache,
        private readonly bool $enabled,
        private readonly int $pathTtl,
        private readonly int $routeTtl,
        private readonly int $missingTtl,
        private readonly string $prefix = 'gateway',
        private readonly bool $cacheCustomProviders = false,
    ) {
    }

    public function resolvePath(PathResolver $resolver, IngressRequest $request): ?PathBinding
    {
        if (! $this->available() || $this->pathTtl <= 0 || ! $resolver instanceof CacheablePathResolver) {
            return $resolver->resolve($request);
        }

        $resolverKey = trim($resolver->cacheKey($request));
        if ($resolverKey === '') {
            return $resolver->resolve($request);
        }

        $key = $this->itemKey('path', [
            $this->generation('paths'),
            $resolver::class,
            $resolverKey,
        ]);

        try {
            $cached = $this->cache?->get($key);
            if (is_array($cached)) {
                return $this->pathBindingFromArray($cached);
            }
        } catch (Throwable) {
            return $resolver->resolve($request);
        }

        $binding = $resolver->resolve($request);

        try {
            $this->cache?->put($key, $this->pathBindingToArray($binding), $this->pathTtl);
        } catch (Throwable) {
            // Cache outages must not prevent request planning.
        }

        return $binding;
    }

    /**
     * @param list<string> $registries
     * @param Closure(): CachedRegistryRoute $loader
     */
    public function resolveRoute(
        string $endpointKey,
        array $registries,
        string $scope,
        string $routeKey,
        Closure $loader,
    ): CachedRegistryRoute {
        if (! $this->available() || $this->routeTtl <= 0) {
            return $loader();
        }

        $key = $this->itemKey('route', [
            $this->generation('endpoint:'.$this->digest($endpointKey)),
            $endpointKey,
            implode(',', $registries),
            $scope,
            $routeKey,
        ]);

        try {
            $cached = $this->cache?->get($key);
            if (is_array($cached)) {
                return CachedRegistryRoute::fromArray($cached);
            }
        } catch (Throwable) {
            return $loader();
        }

        $route = $loader();
        if (! $route->cacheable) {
            return $route;
        }

        try {
            $ttl = $route->found ? $this->routeTtl : min($this->routeTtl, $this->missingTtl);
            if ($ttl > 0) {
                $this->cache?->put($key, $route->toArray(), $ttl);
            }
        } catch (Throwable) {
            // Fall back to the freshly loaded route.
        }

        return $route;
    }

    public function supportsProvider(object $provider): bool
    {
        return $this->cacheCustomProviders || $provider instanceof DatabaseEndpointProvider;
    }

    public function invalidatePaths(): void
    {
        $this->rotateGeneration('paths');
    }

    public function invalidateEndpoint(string $endpointKey): void
    {
        if ($endpointKey !== '') {
            $this->rotateGeneration('endpoint:'.$this->digest($endpointKey));
        }
    }

    private function available(): bool
    {
        return $this->enabled && $this->cache !== null;
    }

    private function generation(string $scope): string
    {
        if (! $this->available()) {
            return '0';
        }

        try {
            $value = $this->cache?->get($this->generationKey($scope), '0');

            return is_string($value) && $value !== '' ? $value : '0';
        } catch (Throwable) {
            return '0';
        }
    }

    private function rotateGeneration(string $scope): void
    {
        if (! $this->available()) {
            return;
        }

        try {
            $this->cache?->forever($this->generationKey($scope), bin2hex(random_bytes(16)));
        } catch (Throwable) {
            // Entries have bounded TTLs, so failed invalidation self-heals.
        }
    }

    private function generationKey(string $scope): string
    {
        return $this->prefix.':generation:'.$scope;
    }

    /** @param list<string> $parts */
    private function itemKey(string $type, array $parts): string
    {
        return $this->prefix.':'.$type.':'.$this->digest(implode("\0", $parts));
    }

    private function digest(string $value): string
    {
        return hash('sha256', $value);
    }

    /** @return array<string, mixed> */
    private function pathBindingToArray(?PathBinding $binding): array
    {
        if ($binding === null) {
            return ['version' => 1, 'found' => false];
        }

        return [
            'version' => 1,
            'found' => true,
            'endpoint_key' => $binding->endpointKey,
            'scope' => $binding->scope,
            'key' => $binding->key,
            'channel' => $binding->channel,
            'destination_metadata' => $binding->destinationMetadata,
        ];
    }

    /** @param array<string, mixed> $value */
    private function pathBindingFromArray(array $value): ?PathBinding
    {
        if (($value['version'] ?? null) !== 1 || ! is_bool($value['found'] ?? null)) {
            throw new \InvalidArgumentException('Cached path binding is malformed.');
        }

        if ($value['found'] === false) {
            return null;
        }

        if (! is_string($value['endpoint_key'] ?? null)
            || ! is_string($value['scope'] ?? null)
            || ! is_string($value['key'] ?? null)
            || (! is_null($value['channel'] ?? null) && ! is_string($value['channel']))
            || ! is_array($value['destination_metadata'] ?? null)) {
            throw new \InvalidArgumentException('Cached path binding is malformed.');
        }

        return new PathBinding(
            endpointKey: $value['endpoint_key'],
            scope: $value['scope'],
            key: $value['key'],
            channel: $value['channel'] ?? null,
            destinationMetadata: $value['destination_metadata'],
        );
    }
}
