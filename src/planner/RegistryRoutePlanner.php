<?php

declare(strict_types=1);

namespace Webong\Gateway;

use InvalidArgumentException;
use RuntimeException;
use Webong\WebProxy\DestinationRecord;
use Webong\WebProxy\DispatchWebhookProxyDestination;
use Webong\WebProxy\Enums\WebhookProxyTargetType;
use Webong\WebProxy\WebProxyChannelManager;
use Webong\WebProxy\WebProxyRegistryManager;
use Webong\WebProxy\WebhookRoute;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Response;
use Webong\Gateway\Protocol\RoutePlan;

/**
 * Default bridge planner for webong/web-proxy.
 *
 * Path ownership stays with the host application through PathResolver. Once
 * a path is bound, this class performs endpoint lookup and destination
 * selection in PHP, dispatching PHP-native handlers locally and serializing
 * only HTTP destinations for Go to execute.
 */
final class RegistryRoutePlanner implements RoutePlanner
{
    public function __construct(
        private readonly PathResolver $pathResolver,
        private readonly WebProxyRegistryManager $registryManager,
        private readonly WebProxyChannelManager $channelManager,
        private readonly SubscriptionMatcher $subscriptionMatcher,
        private readonly RegistryRouteCache $routeCache,
        private readonly ?DispatchWebhookProxyDestination $destinationDispatcher = null,
    ) {
    }

    public function plan(IngressRequest $request): RoutePlan
    {
        $binding = $this->routeCache->resolvePath($this->pathResolver, $request);

        if ($binding === null) {
            return RoutePlan::passThrough();
        }

        $route = new WebhookRoute(
            scope: $binding->scope,
            key: $binding->routeKey($request),
            payload: $this->payload($request),
            headers: $this->headers($request),
            destinationMetadata: $binding->destinationMetadata,
        );
        $payload = $route->payload;
        $headers = $route->headers;
        $registries = $this->channelManager->registries($binding->channel);
        $cachedRoute = $this->routeCache->resolveRoute(
            endpointKey: $binding->endpointKey,
            registries: $registries,
            scope: $route->scope,
            routeKey: $route->key,
            loader: function () use ($binding, $registries, $route): CachedRegistryRoute {
                $resolved = $this->registryManager->resolveByKey($binding->endpointKey, $registries);

                if ($resolved === null) {
                    return CachedRegistryRoute::missing();
                }

                $provider = $resolved->registrar->provider();

                return CachedRegistryRoute::found(
                    registry: $resolved->registry,
                    endpointId: $resolved->endpoint->record->id,
                    endpointKey: $resolved->endpoint->record->endpoint_key,
                    destinations: $provider->destinationsFor(
                        $resolved->endpoint->record,
                        $route,
                    )->values()->all(),
                    cacheable: $this->routeCache->supportsProvider($provider),
                );
            },
        );

        if (! $cachedRoute->found) {
            return RoutePlan::respond(new Response(
                statusCode: 404,
                body: 'Registered endpoint not found.',
            ));
        }

        $provider = $this->registryManager->registry($cachedRoute->registry)->provider();
        $destinations = $cachedRoute->destinations;

        $reply = null;
        $relays = [];

        foreach ($destinations as $destination) {
            if (! $destination instanceof DestinationRecord) {
                throw new RuntimeException('Registry route cache returned an invalid destination.');
            }

            if (! $this->matchesMetadata($destination->metadata, $binding->destinationMetadata)) {
                continue;
            }

            if (! $this->subscriptionMatcher->matches($request, $destination->metadata)) {
                continue;
            }

            if ($destination->target_type !== WebhookProxyTargetType::REQUEST) {
                if ($this->destinationDispatcher === null) {
                    throw new RuntimeException('PHP destination dispatcher is not configured.');
                }

                $this->destinationDispatcher->handle(
                    provider: $provider,
                    destination: $destination,
                    sourceUrl: $this->sourceUrl($request),
                    payload: $payload,
                    headers: $headers,
                    deliveryId: $this->phpDeliveryId($request, $destination->id),
                );

                continue;
            }

            $delivery = new Delivery(
                url: $destination->target,
                subscriberId: $this->subscriberId($destination->owner_id, $destination->metadata),
                id: $destination->id,
                method: $request->method,
                rawQuery: $request->rawQuery,
            );
            $mode = (string) ($destination->metadata['delivery_mode'] ?? 'relay');

            if ($mode === 'reply') {
                if ($reply !== null) {
                    throw new InvalidArgumentException('A route cannot have more than one synchronous reply destination.');
                }

                $reply = $delivery;
                continue;
            }

            if ($mode !== 'relay') {
                throw new InvalidArgumentException("Unsupported delivery mode [{$mode}].");
            }

            $relays[] = $delivery;
        }

        if ($reply === null && $relays === []) {
            return RoutePlan::respond(new Response(statusCode: 204));
        }

        return RoutePlan::relay(
            reply: $reply,
            relays: $relays,
            metadata: [
                'endpoint_id' => $cachedRoute->endpointId,
                'endpoint_key' => $cachedRoute->endpointKey,
            ],
        );
    }

    /** @return array<string, mixed> */
    private function payload(IngressRequest $request): array
    {
        if ($request->body === '') {
            return [];
        }

        try {
            $decoded = json_decode($request->body, true, 512, JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return ['_raw_body' => $request->body];
        }

        return is_array($decoded) ? $decoded : ['value' => $decoded];
    }

    /** @return array<string, string> */
    private function headers(IngressRequest $request): array
    {
        return array_map(
            static fn (array $values): string => implode(',', $values),
            $request->headers,
        );
    }

    /** @param array<string, mixed> $metadata */
    private function subscriberId(string $fallback, array $metadata): string
    {
        $subscriberId = $metadata['subscriber_id'] ?? $fallback;

        return is_scalar($subscriberId) ? (string) $subscriberId : $fallback;
    }

    private function sourceUrl(IngressRequest $request): string
    {
        $forwardedProto = $request->headers['X-Forwarded-Proto'][0] ?? null;
        $scheme = in_array($request->scheme, ['http', 'https'], true)
            ? $request->scheme
            : (is_string($forwardedProto) && in_array($forwardedProto, ['http', 'https'], true)
                ? $forwardedProto
                : 'https');
        $url = $scheme.'://'.$request->host.$request->path;

        return $request->rawQuery === '' ? $url : $url.'?'.$request->rawQuery;
    }

    private function phpDeliveryId(IngressRequest $request, string $destinationId): string
    {
        return hash('sha256', $request->deliveryId.'\n'.$destinationId);
    }

    /** @param array<string, mixed> $metadata @param array<string, mixed> $criteria */
    private function matchesMetadata(array $metadata, array $criteria): bool
    {
        foreach ($criteria as $key => $expected) {
            if (data_get($metadata, (string) $key) !== $expected) {
                return false;
            }
        }

        return true;
    }
}
