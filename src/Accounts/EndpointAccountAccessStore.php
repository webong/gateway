<?php

declare(strict_types=1);

namespace Webong\Gateway\Accounts;

use InvalidArgumentException;
use Webong\Gateway\Exceptions\EndpointNotFoundException;
use Webong\Gateway\Resources\PlannerResourceStore;
use Webong\Proxy\Models\WebProxyEndpoint;
use stdClass;

final readonly class EndpointAccountAccessStore
{
    public const string KIND = 'registry.endpoint-access';

    public function __construct(
        private AccountStore $accounts,
        private PlannerResourceStore $resources,
    ) {
    }

    public function grant(string $endpointKey, string $accountId, string $routingScope, string $routingKey): stdClass
    {
        $this->assertRoute($routingScope, $routingKey);
        if (! $this->accounts->exists($accountId)
            || ! WebProxyEndpoint::query()->where('endpoint_key', $endpointKey)->exists()) {
            throw new EndpointNotFoundException('The endpoint or account was not found.');
        }

        $key = $this->resourceKey($endpointKey, $routingScope, $routingKey);
        $document = [
            'endpoint_key' => $endpointKey,
            'routing_scope' => $routingScope,
            'routing_key' => $routingKey,
        ];
        $existing = $this->resources->byOwnedKey(self::KIND, $accountId, $key);
        if ($existing !== null) {
            if ($existing->enabled) {
                return $existing;
            }

            return $this->resources->replace(
                self::KIND,
                $existing->id,
                $key,
                $document,
                true,
                $existing->revision,
                $accountId,
            ) ?? throw new EndpointNotFoundException('The endpoint access grant was not found.');
        }

        return $this->resources->create(self::KIND, $key, $document, true, owner: $accountId);
    }

    public function revoke(string $endpointKey, string $accountId, string $routingScope, string $routingKey): bool
    {
        $this->assertRoute($routingScope, $routingKey);
        $key = $this->resourceKey($endpointKey, $routingScope, $routingKey);
        $existing = $this->resources->byOwnedKey(self::KIND, $accountId, $key);
        if ($existing === null || ! $existing->enabled) {
            return false;
        }

        return $this->resources->replace(
            self::KIND,
            $existing->id,
            $key,
            $existing->document,
            false,
            $existing->revision,
            $accountId,
        ) !== null;
    }

    public function allows(
        string $endpointKey,
        string $accountId,
        string $routingScope,
        string $routingKey,
    ): bool {
        $this->assertRoute($routingScope, $routingKey);
        $grant = $this->resources->byOwnedKey(
            self::KIND,
            $accountId,
            $this->resourceKey($endpointKey, $routingScope, $routingKey),
        );

        return $grant !== null && $grant->enabled;
    }

    private function resourceKey(string $endpointKey, string $routingScope, string $routingKey): string
    {
        return $endpointKey.':'.hash('sha256', $routingScope."\n".$routingKey);
    }

    private function assertRoute(string $routingScope, string $routingKey): void
    {
        if (trim($routingScope) === '' || strlen($routingScope) > 255
            || trim($routingKey) === '' || strlen($routingKey) > 255) {
            throw new InvalidArgumentException('A valid routing scope and routing key are required.');
        }
    }
}
