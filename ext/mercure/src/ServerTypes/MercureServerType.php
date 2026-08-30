<?php

declare(strict_types=1);

namespace Webong\Gateway\Mercure\ServerTypes;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Validation\Rule;
use Illuminate\Validation\ValidationException;
use Webong\Gateway\Servers\Contracts\ServerType;
use Webong\Gateway\Servers\Models\Server;

final readonly class MercureServerType implements ServerType
{
    public function __construct(private ValidationFactory $validator) {}

    public function type(): string
    {
        return 'mercure';
    }

    public function defaultPath(): string
    {
        return '/.well-known/mercure';
    }

    public function supportsApplications(): bool
    {
        return false;
    }

    public function validateConfiguration(array $configuration, ?Server $server, int $replicas): array
    {
        $this->rejectUnsupported($configuration, ['publisher_jwt', 'subscriber_jwt', 'anonymous', 'cors_origins', 'publish_origins', 'subscriptions', 'heartbeat', 'transport'], 'configuration');
        foreach (['publisher_jwt', 'subscriber_jwt'] as $key) {
            if (isset($configuration[$key]) && is_array($configuration[$key])) {
                $this->rejectUnsupported($configuration[$key], ['key', 'algorithm'], 'configuration.'.$key);
            }
        }
        $required = $server === null ? 'required' : 'sometimes';

        $validated = $this->validator->make([
            'configuration' => $configuration,
            'replicas' => $replicas,
        ], [
            'replicas' => ['integer', Rule::in([1])],
            'configuration' => ['array'],
            'configuration.publisher_jwt' => [$required, 'array'],
            'configuration.publisher_jwt.key' => [$required, 'string', 'max:16384'],
            'configuration.publisher_jwt.algorithm' => ['sometimes', Rule::in(['HS256', 'HS384', 'HS512', 'RS256', 'RS384', 'RS512'])],
            'configuration.subscriber_jwt' => [$required, 'array'],
            'configuration.subscriber_jwt.key' => [$required, 'string', 'max:16384'],
            'configuration.subscriber_jwt.algorithm' => ['sometimes', Rule::in(['HS256', 'HS384', 'HS512', 'RS256', 'RS384', 'RS512'])],
            'configuration.anonymous' => ['sometimes', 'boolean'],
            'configuration.cors_origins' => ['sometimes', 'array', 'max:128'],
            'configuration.cors_origins.*' => ['string', 'max:2048', 'regex:/^(\*|https?:\/\/[^\s]+)$/'],
            'configuration.publish_origins' => ['sometimes', 'array', 'max:128'],
            'configuration.publish_origins.*' => ['string', 'max:2048', 'regex:/^(\*|https?:\/\/[^\s]+)$/'],
            'configuration.subscriptions' => ['sometimes', 'boolean'],
            'configuration.heartbeat' => ['sometimes', 'string', 'regex:/^\d+(ms|s|m|h)$/'],
            'configuration.transport' => ['sometimes', Rule::in(['local'])],
        ])->validate();

        return $validated['configuration'] ?? [];
    }

    public function configurationForStorage(array $validated, array $current, string $serverId): array
    {
        $configuration = [
            'publisher_jwt_algorithm' => 'HS256',
            'subscriber_jwt_algorithm' => 'HS256',
            'anonymous' => false,
            'cors_origins' => [],
            'publish_origins' => [],
            'subscriptions' => false,
            'heartbeat' => '40s',
            'transport' => 'local',
            ...$current,
        ];
        foreach (['publisher_jwt', 'subscriber_jwt'] as $name) {
            if (! isset($validated[$name])) {
                continue;
            }
            $jwt = $validated[$name];
            unset($validated[$name]);
            foreach (['key', 'algorithm'] as $property) {
                if (array_key_exists($property, $jwt)) {
                    $configuration[$name.'_'.$property] = $jwt[$property];
                }
            }
        }

        return [...$configuration, ...$validated];
    }

    public function configurationResource(Server $server): array
    {
        $configuration = $server->configuration ?? [];

        return [
            'publisher_jwt' => [
                'algorithm' => $configuration['publisher_jwt_algorithm'] ?? 'HS256',
                'key_configured' => ($configuration['publisher_jwt_key'] ?? '') !== '',
            ],
            'subscriber_jwt' => [
                'algorithm' => $configuration['subscriber_jwt_algorithm'] ?? 'HS256',
                'key_configured' => ($configuration['subscriber_jwt_key'] ?? '') !== '',
            ],
            'anonymous' => (bool) ($configuration['anonymous'] ?? false),
            'cors_origins' => $configuration['cors_origins'] ?? [],
            'publish_origins' => $configuration['publish_origins'] ?? [],
            'subscriptions' => (bool) ($configuration['subscriptions'] ?? false),
            'heartbeat' => $configuration['heartbeat'] ?? '40s',
            'transport' => $configuration['transport'] ?? 'local',
        ];
    }

    private function rejectUnsupported(array $input, array $supported, string $key): void
    {
        $unsupported = array_values(array_diff(array_keys($input), $supported));
        if ($unsupported !== []) {
            throw ValidationException::withMessages([$key => ['Unsupported properties: '.implode(', ', $unsupported).'.']]);
        }
    }
}
