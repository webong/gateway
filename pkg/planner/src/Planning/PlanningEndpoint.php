<?php

declare(strict_types=1);

namespace Webong\Gateway\Planning;

use InvalidArgumentException;
use JsonException;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;

/** Shared authenticated Spinner interface for Laravel and Symfony hosts. */
final readonly class PlanningEndpoint
{
    public function __construct(private string $internalToken, private int $maxBodyBytes = 1048576)
    {
        if ($maxBodyBytes < 1) {
            throw new InvalidArgumentException('Planning body limit must be positive.');
        }
    }

    public function plan(string $method, string $token, string $body, RoutePlanner $planner): PlanningResponse
    {
        return $this->dispatch($method, $token, $body, IngressRequest::fromArray(...), static fn (IngressRequest $request): array => $planner->plan($request)->toArray());
    }

    public function event(string $method, string $token, string $body, ProtocolPlanner $planner): PlanningResponse
    {
        return $this->dispatch($method, $token, $body, GatewayEvent::fromArray(...), static fn (GatewayEvent $event): array => $planner->planEvent($event)->toArray());
    }

    private function dispatch(string $method, string $provided, string $body, callable $decode, callable $plan): PlanningResponse
    {
        if ($this->internalToken === '' || $provided === '' || ! hash_equals($this->internalToken, $provided)) {
            return new PlanningResponse(['message' => 'Not Found'], 404);
        }
        if ($method !== 'POST') {
            return new PlanningResponse(['message' => 'Method Not Allowed'], 405, ['Allow' => 'POST']);
        }
        if (strlen($body) > $this->maxBodyBytes) {
            return new PlanningResponse(['message' => 'Payload Too Large'], 413);
        }
        try {
            $payload = json_decode($body, true, 64, JSON_THROW_ON_ERROR);
            if (! is_array($payload) || ! str_starts_with(ltrim($body), '{')) {
                throw new InvalidArgumentException('Expected a JSON object.');
            }
            $input = $decode($payload);
        } catch (JsonException|InvalidArgumentException|\TypeError|\ValueError $exception) {
            return new PlanningResponse(['message' => 'Invalid planning request'], 422);
        }

        return new PlanningResponse($plan($input));
    }
}
