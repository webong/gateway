<?php

declare(strict_types=1);

namespace Webong\Gateway\Web;

use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayDelivery;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\RouteProtocolPlanner;
use Webong\Gateway\Webstream\WebstreamPrivateSourceStore;
use Webong\Gateway\Webstream\WebstreamRoutePolicyStore;

final class WebProtocolPlanner extends RouteProtocolPlanner
{
    public function __construct(
        \Webong\Gateway\Contracts\RoutePlanner $routePlanner,
        private readonly WebstreamPrivateSourceStore $privateSources,
        private readonly WebstreamRoutePolicyStore $routePolicies,
    ) {
        parent::__construct($routePlanner);
    }

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        if ($event->protocol !== Protocol::WEBSTREAM) {
            return parent::planEvent($event);
        }

        if (($event->attributes['upload_mode'] ?? '') === 'stream') {
            $policy = $this->webstreamPolicy($event);
            if (($policy['upload'] ?? false) !== true || $event->method !== 'POST' || $event->payload !== '') {
                return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT,
                    statusCode: 403, message: 'Streaming uploads are not enabled for this route.');
            }
        }

        $plan = $this->routePlanner->plan($this->request($event));

        return match ($plan->action) {
            RoutePlan::RESPOND => $this->respond($event, $plan),
            RoutePlan::RELAY => $this->stream($event, $plan),
            RoutePlan::PASS_THROUGH => new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'No registered gateway route.',
            ),
        };
    }

    private function stream(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        if ($plan->reply === null) {
            return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT,
                statusCode: 404, message: 'No Webstream source is registered.');
        }

        $source = $this->delivery($plan->reply, $event);
        $method = strtoupper($event->method ?: 'GET');
        if (! in_array($method, ['GET', 'POST'], true)
            || ($method === 'POST' && $plan->reply->method !== '' && strtoupper($plan->reply->method) !== 'POST')) {
            return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT,
                statusCode: 405, message: 'Webstream source method is not permitted.');
        }

        $headers = $source->headers;
        $upload = ($event->attributes['upload_mode'] ?? '') === 'stream';
        $policy = $this->webstreamPolicy($event);
        $retry = ! $upload && $method === 'POST' && ($policy['retry'] ?? false) === true;
        if (($upload || $retry) && (($policy['source'] ?? '') !== $source->target
            || ($upload && $plan->reply->body !== ''))) {
            return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT,
                statusCode: 403, message: 'Webstream policy does not permit this source.');
        }

        if ($retry) {
            $keys = [];
            foreach ($event->headers as $name => $values) {
                if (strcasecmp($name, 'Idempotency-Key') === 0) {
                    $keys = [...$keys, ...$values];
                }
            }
            if (count($keys) !== 1 || trim($keys[0]) === '' || strlen($keys[0]) > 255) {
                return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT,
                    statusCode: 400, message: 'One Idempotency-Key is required.');
            }
            $headers = array_filter($headers,
                static fn (string $name): bool => strcasecmp($name, 'Idempotency-Key') !== 0,
                ARRAY_FILTER_USE_KEY);
            $headers['Idempotency-Key'] = $keys;
        }

        if ($method === 'POST') {
            $hasContentType = false;
            foreach ($headers as $name => $values) {
                $hasContentType = $hasContentType || strcasecmp($name, 'Content-Type') === 0;
            }
            foreach ($event->headers as $name => $values) {
                if (! $hasContentType && strcasecmp($name, 'Content-Type') === 0) {
                    $headers['Content-Type'] = $values;
                }
            }
        }

        $lastEventId = null;
        foreach ($event->headers as $name => $values) {
            if (strcasecmp($name, 'Last-Event-ID') === 0) {
                $lastEventId = $values[0] ?? null;
                break;
            }
        }
        if ($lastEventId !== null) {
            $headers = array_filter($headers,
                static fn (string $name): bool => strcasecmp($name, 'Last-Event-ID') !== 0,
                ARRAY_FILTER_USE_KEY);
            $headers['Last-Event-ID'] = [$lastEventId];
        }

        $source = new GatewayDelivery(
            protocol: $source->protocol,
            target: $source->target,
            subscriberId: $source->subscriberId,
            headers: $headers,
            attributes: [
                ...$source->attributes,
                'method' => $method,
                'raw_query' => $plan->reply->rawQuery !== '' ? $plan->reply->rawQuery : $event->rawQuery,
                'upload_mode' => $upload ? 'stream' : 'buffered',
                'retry_idempotent' => $retry ? 'true' : 'false',
                'private_source_cidrs' => $this->privateSourceCidrs($source->target),
            ],
            payload: $method === 'POST' ? $source->payload : '',
            adapter: $source->adapter,
            transport: $source->transport,
        );

        return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::STREAM,
            stream: $source, metadata: $plan->metadata);
    }

    private function privateSourceCidrs(string $target): string
    {
        $parts = parse_url($target);
        if ($parts === false || ! isset($parts['scheme'], $parts['host'], $parts['port'])) {
            return '';
        }
        $origin = strtolower($parts['scheme']).'://'.strtolower($parts['host']).':'.$parts['port'];

        return implode(',', $this->privateSources->activeCidrs($origin));
    }

    /** @return array{source?: string, upload?: bool, retry?: bool} */
    private function webstreamPolicy(GatewayEvent $event): array
    {
        $stored = $this->routePolicies->forRoute($event->host, $event->route);

        return $stored !== null && (bool) $stored->enabled ? [
            'source' => $stored->source,
            'upload' => (bool) $stored->upload,
            'retry' => (bool) $stored->retry,
        ] : [];
    }
}
