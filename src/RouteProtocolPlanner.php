<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayDelivery;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Protocols\RoutePlan;

/** Default protocol-neutral adapter from a Gateway event to registry routing. */
class RouteProtocolPlanner implements ProtocolPlanner
{
    public function __construct(protected readonly RoutePlanner $routePlanner) {}

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        $plan = $this->routePlanner->plan($this->request($event));

        return match ($plan->action) {
            RoutePlan::RESPOND => $this->respond($event, $plan),
            RoutePlan::RELAY => $this->deliver($event, $plan),
            RoutePlan::AUTOMATION => $this->automate($event, $plan),
            RoutePlan::PASS_THROUGH => new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'No registered gateway route.',
            ),
        };
    }

    protected function request(GatewayEvent $event): IngressRequest
    {
        return new IngressRequest(
            deliveryId: $event->id,
            method: $event->method !== '' ? $event->method : 'POST',
            host: $event->host,
            path: $event->route,
            rawQuery: $event->rawQuery,
            headers: $event->headers,
            body: $event->payload,
            scheme: 'https',
            protocol: $event->protocol->value,
            event: $event->kind->value,
            sessionId: $event->sessionId,
            attributes: $event->attributes,
        );
    }

    protected function respond(GatewayEvent $event, RoutePlan $plan): GatewayDecision
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

    protected function deliver(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $deliveries = array_map(
            fn (Delivery $delivery): GatewayDelivery => $this->delivery($delivery, $event),
            array_filter([$plan->reply, ...$plan->relays]),
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

    protected function automate(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $deliveries = array_map(
            fn (Delivery $delivery): GatewayDelivery => $this->delivery($delivery, $event),
            $plan->relays,
        );

        return new GatewayDecision(
            protocol: $event->protocol,
            action: GatewayAction::AUTOMATE,
            statusCode: 250,
            deliveries: array_values($deliveries),
            metadata: $plan->metadata,
            automation: $plan->automation,
        );
    }

    protected function delivery(Delivery $delivery, GatewayEvent $event): GatewayDelivery
    {
        return new GatewayDelivery(
            protocol: Protocol::HTTP,
            target: $delivery->url,
            subscriberId: $delivery->subscriberId,
            headers: $delivery->headers,
            attributes: array_filter([
                'delivery_id' => $delivery->id,
                'event_id' => $event->id,
                'method' => $delivery->method,
                'raw_query' => $delivery->rawQuery,
            ], static fn (mixed $value): bool => $value !== ''),
            payload: $delivery->body !== '' ? $delivery->body : $event->payload,
            transport: $delivery->transport,
        );
    }
}
