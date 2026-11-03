<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use Webong\Gateway\CachedRegistryRoute;
use Webong\Gateway\RegistryRouteCache;
use Webong\Gateway\SubscriptionDefinition;
use Webong\Gateway\SubscriptionMatcher;
use Webong\Gateway\SubscriptionTargetExtensionRegistry;
use Webong\Gateway\Web\EndpointHttpTransportResolver;
use Webong\Gateway\Web\WebSubscriptionTargetExtension;
use InvalidArgumentException;
use RuntimeException;
use Webong\Proxy\DestinationRecord;
use Webong\Proxy\DispatchWebhookProxyDestination;
use Webong\Proxy\Enums\WebhookProxyTargetType;
use Webong\Proxy\WebProxyChannelManager;
use Webong\Proxy\WebProxyRegistryManager;
use Webong\Proxy\WebhookRoute;
use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Contracts\SubscriptionDeliveryTargetExtension;
use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\HttpTransport;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;

/**
 * Default bridge planner for webong/proxy.
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
        private readonly EndpointHttpTransportResolver $transportResolver,
        private readonly SubscriptionTargetExtensionRegistry $targetExtensions,
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
        $automationTarget = null;
        $relays = [];
        $profileEndpoint = null;
        /** @var array<string, HttpTransport> $profileTransports */
        $profileTransports = [];

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

            $targetExtension = $destination->target_type === WebhookProxyTargetType::REQUEST
                ? $this->targetExtensions->forDestination($destination)
                : null;
            if ($targetExtension !== null) {
                $targetPlan = $targetExtension->plan($destination, $cachedRoute->endpointKey, $request->protocol);
                if ($targetPlan !== null) {
                    if (! $targetExtension->supportsProtocol($request->protocol)) {
                        throw new InvalidArgumentException('The subscription target does not support this ingress protocol.');
                    }
                    if (($destination->metadata['delivery_mode'] ?? null) !== 'reply' || $automationTarget !== null) {
                        throw new InvalidArgumentException('Extension automation targets must be a single reply target.');
                    }
                    $automationTarget = $targetPlan;
                    continue;
                }
                if ($targetExtension->kind() !== WebSubscriptionTargetExtension::KIND
                    && ! ($targetExtension instanceof SubscriptionDeliveryTargetExtension)) {
                    throw new RuntimeException('The subscription target extension did not provide a route plan.');
                }
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

            $targetOptions = $destination->metadata[SubscriptionDefinition::TARGET_OPTIONS_METADATA_KEY] ?? null;
            if ($targetOptions === null && is_string($destination->metadata['_gateway_profile'] ?? null)) {
                $targetOptions = ['profile' => $destination->metadata['_gateway_profile']];
            }
            $targetOptions ??= [];
            if (! is_array($targetOptions)) { throw new InvalidArgumentException('Subscription target options are malformed.'); }
            $profile = $targetOptions['profile'] ?? null;
            if ($profile !== null && (! is_string($profile) || $profile === '')) {
                throw new InvalidArgumentException('Subscription proxy profile is malformed.');
            }
            if ($profile !== null && $profileEndpoint === null) {
                $profileEndpoint = $provider->resolveById($cachedRoute->endpointId)
                    ?? throw new RuntimeException('Registry endpoint disappeared while resolving its proxy profile.');
            }
            $transport = $profile === null
                ? null
                : ($profileTransports[$profile] ??= $this->transportResolver->resolve($profileEndpoint, $profile));
            $deliveryProtocol = $targetExtension instanceof SubscriptionDeliveryTargetExtension
                ? $targetExtension->deliveryProtocol()
                : Protocol::HTTP;
            if ($targetExtension instanceof SubscriptionDeliveryTargetExtension
                && ! $targetExtension->supportsProtocol($request->protocol)) {
                throw new InvalidArgumentException('The subscription target does not support this ingress protocol.');
            }

            $delivery = new Delivery(
                url: $destination->target,
                subscriberId: $this->subscriberId($destination->owner_id, $destination->metadata),
                id: $destination->id,
                method: $request->method,
                rawQuery: $request->rawQuery,
                transport: $transport,
                protocol: $deliveryProtocol,
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

        if ($automationTarget !== null) {
            if ($reply !== null) {
                throw new InvalidArgumentException('A route cannot have more than one synchronous reply destination.');
            }

            return RoutePlan::automate(
                automation: $automationTarget->automation,
                relays: $relays,
                metadata: [
                    'endpoint_id' => $cachedRoute->endpointId,
                    'endpoint_key' => $cachedRoute->endpointKey,
                    ...$automationTarget->metadata,
                ],
            );
        }

        if ($reply === null && $relays === []) {
            return RoutePlan::respond(new Response(statusCode: 204), metadata: [
                'endpoint_id' => $cachedRoute->endpointId,
                'endpoint_key' => $cachedRoute->endpointKey,
            ]);
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
