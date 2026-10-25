<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns;

use Webong\Gateway\Protocols\Delivery;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayDelivery;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\RouteProtocolPlanner;

final class DnsProtocolPlanner extends RouteProtocolPlanner
{
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
            scheme: 'dns',
            protocol: $event->protocol->value,
            event: $event->kind->value,
            sessionId: $event->sessionId,
            attributes: $event->attributes,
            automationLanguages: $event->automationLanguages,
        );
    }

    protected function deliver(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $reply = $plan->reply === null ? null : $this->delivery($plan->reply, $event);
        $deliveries = array_map(
            fn (Delivery $delivery): GatewayDelivery => $this->delivery($delivery, $event),
            $plan->relays,
        );

        if ($reply === null && $deliveries === []) {
            return new GatewayDecision(protocol: Protocol::DNS, action: GatewayAction::ACCEPT, statusCode: 250);
        }

        return new GatewayDecision(
            protocol: Protocol::DNS,
            action: GatewayAction::DELIVER,
            statusCode: 250,
            reply: $reply,
            deliveries: array_values($deliveries),
            metadata: $plan->metadata,
        );
    }

    protected function delivery(Delivery $delivery, GatewayEvent $event): GatewayDelivery
    {
        $headers = $delivery->headers;
        $headers['Content-Type'] ??= ['application/json'];

        return new GatewayDelivery(
            protocol: Protocol::HTTP,
            target: $delivery->url,
            subscriberId: $delivery->subscriberId,
            headers: $headers,
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
