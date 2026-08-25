<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayDelivery;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Protocol;
use Webong\Gateway\Protocol\RoutePlan;

/**
 * Adapts the existing PHP registry planner to protocol-neutral events.
 *
 * HTTP destinations remain the first supported egress for SMTP/WebSocket
 * events. The original event payload is carried to each destination so the
 * Go transport can deliver it without PHP owning network I/O.
 */
final class RegistryProtocolPlanner implements ProtocolPlanner
{
    public function __construct(private readonly RoutePlanner $routePlanner)
    {
    }

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        $plan = $this->routePlanner->plan(new IngressRequest(
            deliveryId: $event->id,
            method: $event->method !== '' ? $event->method : 'POST',
            host: $event->host,
            path: $event->route,
            rawQuery: $event->rawQuery,
            headers: $event->headers,
            body: $event->payload,
            scheme: $event->protocol === Protocol::SMTP ? 'smtp' : 'https',
            protocol: $event->protocol->value,
            event: $event->kind->value,
            sessionId: $event->sessionId,
        ));

        return match ($plan->action) {
            RoutePlan::RESPOND => $this->respond($event, $plan),
            RoutePlan::RELAY => $this->deliver($event, $plan),
            RoutePlan::PASS_THROUGH => new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'No registered gateway route.',
            ),
        };
    }

    private function respond(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $response = $plan->immediateResponse;

        return new GatewayDecision(
            protocol: $event->protocol,
            action: GatewayAction::RESPOND,
            statusCode: $response?->statusCode ?? 500,
            headers: $response?->headers ?? [],
            payload: $response?->body ?? '',
        );
    }

    private function deliver(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $sourceDeliveries = array_filter([$plan->reply, ...$plan->relays]);
        $deliveries = array_map(
            fn (Delivery $delivery): GatewayDelivery => new GatewayDelivery(
                protocol: Protocol::HTTP,
                target: $delivery->url,
                subscriberId: $delivery->subscriberId,
                headers: $delivery->headers,
                attributes: array_filter([
                    'delivery_id' => $delivery->id,
                    'method' => $delivery->method,
                    'raw_query' => $delivery->rawQuery,
                ], static fn (mixed $value): bool => $value !== ''),
                payload: $delivery->body !== '' ? $delivery->body : $event->payload,
            ),
            $sourceDeliveries,
        );

        if ($deliveries === []) {
            return new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::ACCEPT,
                statusCode: 250,
            );
        }

        return new GatewayDecision(
            protocol: $event->protocol,
            action: GatewayAction::DELIVER,
            statusCode: 250,
            deliveries: array_values($deliveries),
            metadata: $plan->metadata,
        );
    }
}
