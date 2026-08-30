<?php

declare(strict_types=1);

namespace Webong\Gateway\Centrifugo\ServerTypes;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Contracts\Validation\Validator;
use Illuminate\Validation\Rule;
use Illuminate\Validation\ValidationException;
use Webong\Gateway\Servers\Contracts\ServerType;
use Webong\Gateway\Servers\Models\Server;

final readonly class CentrifugoServerType implements ServerType
{
    public function __construct(private ValidationFactory $validator) {}

    public function type(): string
    {
        return 'centrifugo';
    }

    public function defaultPath(): string
    {
        return '/connection';
    }

    public function supportsApplications(): bool
    {
        return false;
    }

    public function validateConfiguration(array $configuration, ?Server $server, int $replicas): array
    {
        $this->rejectUnsupported($configuration, ['client', 'channel', 'http_api', 'engine', 'prometheus_enabled', 'log_level'], 'configuration');
        $nested = [
            'client' => ['token_hmac_secret_key', 'allowed_origins', 'insecure'],
            'channel' => ['allow_subscribe_for_client'],
            'http_api' => ['key'],
            'engine' => ['type', 'redis_address'],
        ];
        foreach ($nested as $key => $properties) {
            if (isset($configuration[$key]) && is_array($configuration[$key])) {
                $this->rejectUnsupported($configuration[$key], $properties, 'configuration.'.$key);
            }
        }
        $required = $server === null ? 'required' : 'sometimes';
        $validator = $this->validator->make(['configuration' => $configuration], [
            'configuration' => ['array'],
            'configuration.client' => [$required, 'array'],
            'configuration.client.token_hmac_secret_key' => [$required, 'string', 'max:16384'],
            'configuration.client.allowed_origins' => ['sometimes', 'array', 'max:128'],
            'configuration.client.allowed_origins.*' => ['string', 'max:2048', 'regex:/^(\*|https?:\/\/[^\s]+)$/'],
            'configuration.client.insecure' => ['sometimes', 'boolean'],
            'configuration.channel' => ['sometimes', 'array'],
            'configuration.channel.allow_subscribe_for_client' => ['sometimes', 'boolean'],
            'configuration.http_api' => [$required, 'array'],
            'configuration.http_api.key' => [$required, 'string', 'max:4096'],
            'configuration.engine' => ['sometimes', 'array'],
            'configuration.engine.type' => ['sometimes', Rule::in(['memory', 'redis'])],
            'configuration.engine.redis_address' => ['sometimes', 'nullable', 'string', 'max:4096'],
            'configuration.prometheus_enabled' => ['sometimes', 'boolean'],
            'configuration.log_level' => ['sometimes', Rule::in(['trace', 'debug', 'info', 'warn', 'error', 'none'])],
        ]);
        $validator->after(function (Validator $validator) use ($configuration, $server, $replicas): void {
            $current = $server?->configuration ?? [];
            $engine = $configuration['engine']['type'] ?? $current['engine_type'] ?? 'memory';
            $redisAddress = $configuration['engine']['redis_address'] ?? $current['engine_redis_address'] ?? null;
            if ($engine === 'redis' && (! is_string($redisAddress) || trim($redisAddress) === '')) {
                $validator->errors()->add('configuration.engine.redis_address', 'A Redis address is required for the Redis engine.');
            }
            if ($replicas > 1 && $engine !== 'redis') {
                $validator->errors()->add('replicas', 'Multiple Centrifugo replicas require the Redis engine.');
            }
        });

        $validated = $validator->validate();

        return $validated['configuration'] ?? [];
    }

    public function configurationForStorage(array $validated, array $current, string $serverId): array
    {
        $configuration = [
            'client_allowed_origins' => [],
            'client_insecure' => false,
            'channel_without_namespace_allow_subscribe_for_client' => false,
            'engine_type' => 'memory',
            'engine_redis_address' => null,
            'prometheus_enabled' => false,
            'log_level' => 'info',
            ...$current,
        ];
        $mappings = [
            'client' => [
                'token_hmac_secret_key' => 'client_token_hmac_secret_key',
                'allowed_origins' => 'client_allowed_origins',
                'insecure' => 'client_insecure',
            ],
            'channel' => ['allow_subscribe_for_client' => 'channel_without_namespace_allow_subscribe_for_client'],
            'http_api' => ['key' => 'http_api_key'],
            'engine' => ['type' => 'engine_type', 'redis_address' => 'engine_redis_address'],
        ];
        foreach ($mappings as $section => $properties) {
            if (! isset($validated[$section])) {
                continue;
            }
            $values = $validated[$section];
            unset($validated[$section]);
            foreach ($properties as $source => $target) {
                if (array_key_exists($source, $values)) {
                    $configuration[$target] = $values[$source];
                }
            }
        }

        return [...$configuration, ...$validated];
    }

    public function configurationResource(Server $server): array
    {
        $configuration = $server->configuration ?? [];

        return [
            'client' => [
                'token_hmac_secret_key_configured' => ($configuration['client_token_hmac_secret_key'] ?? '') !== '',
                'allowed_origins' => $configuration['client_allowed_origins'] ?? [],
                'insecure' => (bool) ($configuration['client_insecure'] ?? false),
            ],
            'channel' => [
                'allow_subscribe_for_client' => (bool) ($configuration['channel_without_namespace_allow_subscribe_for_client'] ?? false),
            ],
            'http_api' => ['key_configured' => ($configuration['http_api_key'] ?? '') !== ''],
            'engine' => [
                'type' => $configuration['engine_type'] ?? 'memory',
                'redis_address_configured' => ($configuration['engine_redis_address'] ?? '') !== '',
            ],
            'prometheus_enabled' => (bool) ($configuration['prometheus_enabled'] ?? false),
            'log_level' => $configuration['log_level'] ?? 'info',
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
