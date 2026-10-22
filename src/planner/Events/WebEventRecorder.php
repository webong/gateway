<?php

declare(strict_types=1);

namespace Webong\Gateway\Events;

use Psr\Log\LoggerInterface;
use Throwable;
use Webong\Gateway\Events\Models\GatewayEndpointEvent;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\RoutePlan;
use Webong\Proxy\Models\WebProxyEndpoint;

/** Bounded, endpoint-owned Web ingress history. Delivery outcomes live elsewhere. */
final readonly class WebEventRecorder
{
    public function __construct(
        private EventRetention $retention,
        private WebEventPolicyStore $policy,
        private ?LoggerInterface $logger = null,
    ) {}

    public function record(IngressRequest $request, RoutePlan $plan): void
    {
        if (! $this->policy->current()['enabled']
            || ! in_array($request->protocol, ['http', 'websocket', 'webstream'], true)
            || ! is_string($plan->metadata['endpoint_id'] ?? null)) {
            return;
        }

        try {
            $this->persist($request, $plan);
            $this->retention->maybePrune();
        } catch (Throwable $exception) {
            // History is observational and must never turn an accepted route
            // into a failed webhook or socket event.
            $this->logger?->warning('Unable to record Gateway Web event.', [
                'event_id' => $request->attributes['ingress_id'] ?? $request->deliveryId,
                'exception' => $exception,
            ]);
        }
    }

    private function persist(IngressRequest $request, RoutePlan $plan): void
    {
        $endpoint = WebProxyEndpoint::query()->find($plan->metadata['endpoint_id']);
        if ($endpoint === null) {
            return;
        }
        $metadata = is_array($endpoint->metadata) ? $endpoint->metadata : [];
        $gateway = is_array($metadata['_gateway'] ?? null) ? $metadata['_gateway'] : [];
        $captureBody = ($gateway['capture_body'] ?? false) === true;
        $bodyBytes = strlen($request->body);
        $payload = [
            'body_bytes' => $bodyBytes,
            'body_sha256' => hash('sha256', $request->body),
        ];
        if ($captureBody) {
            $maxBytes = min(1048576, max(1, (int) config('gateway.events.max_body_bytes', 65536)));
            $payload['body_base64'] = base64_encode(substr($request->body, 0, $maxBytes));
            $payload['truncated'] = $bodyBytes > $maxBytes;
        }
        $headerNames = array_keys($request->headers);
        sort($headerNames);
        $receiptId = $request->attributes['ingress_id'] ?? $request->deliveryId;

        GatewayEndpointEvent::query()->firstOrCreate(
            ['event_id' => $receiptId],
            [
                'endpoint_id' => $endpoint->getKey(),
                'protocol' => $request->protocol,
                'kind' => $request->event,
                'source_address' => ($request->attributes['remote_addr'] ?? '') ?: null,
                'decision_action' => $plan->action,
                'status_code' => $plan->immediateResponse?->statusCode,
                'attributes' => [
                    'method' => $request->method,
                    'path' => $request->path,
                    'query_present' => $request->rawQuery !== '',
                    'header_names' => $headerNames,
                    'session_id' => $request->sessionId !== '' ? $request->sessionId : null,
                    'delivery_id' => $request->deliveryId,
                ],
                'payload' => $payload,
                'received_at' => now(),
            ],
        );
    }
}
