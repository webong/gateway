<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb\ServerTypes;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Validation\Rule;
use Illuminate\Validation\ValidationException;
use Webong\Gateway\Servers\Contracts\ApplicationServerType;
use Webong\Gateway\Servers\Models\Application;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\ServerTables;

final readonly class ReverbServerType implements ApplicationServerType
{
    public function __construct(private ValidationFactory $validator) {}

    public function type(): string
    {
        return 'reverb';
    }

    public function defaultPath(): string
    {
        return '';
    }

    public function supportsApplications(): bool
    {
        return true;
    }

    public function validateConfiguration(array $configuration, ?Server $server, int $replicas): array
    {
        $this->rejectUnsupported($configuration, ['max_request_size', 'scaling', 'observability'], 'configuration');
        if (isset($configuration['scaling']) && is_array($configuration['scaling'])) {
            $this->rejectUnsupported($configuration['scaling'], ['enabled', 'channel', 'server'], 'configuration.scaling');
        }
        if (isset($configuration['scaling']['server']) && is_array($configuration['scaling']['server'])) {
            $this->rejectUnsupported($configuration['scaling']['server'], ['url', 'host', 'port', 'username', 'password', 'database', 'timeout'], 'configuration.scaling.server');
        }
        if (isset($configuration['observability']) && is_array($configuration['observability'])) {
            $this->rejectUnsupported($configuration['observability'], ['pulse_ingest_interval', 'telescope_ingest_interval'], 'configuration.observability');
        }

        $validated = $this->validator->make(['configuration' => $configuration], [
            'configuration' => ['array'],
            'configuration.max_request_size' => ['sometimes', 'integer', 'min:1', 'max:104857600'],
            'configuration.scaling' => ['sometimes', 'array'],
            'configuration.scaling.enabled' => ['sometimes', 'boolean'],
            'configuration.scaling.channel' => ['sometimes', 'string', 'max:255', 'regex:/^[A-Za-z0-9:_.-]+$/'],
            'configuration.scaling.server' => ['sometimes', 'array'],
            'configuration.scaling.server.url' => ['sometimes', 'nullable', 'string', 'max:2048'],
            'configuration.scaling.server.host' => ['sometimes', 'string', 'max:253'],
            'configuration.scaling.server.port' => ['sometimes', 'integer', 'min:1', 'max:65535'],
            'configuration.scaling.server.username' => ['sometimes', 'nullable', 'string', 'max:255'],
            'configuration.scaling.server.password' => ['sometimes', 'nullable', 'string', 'max:4096'],
            'configuration.scaling.server.database' => ['sometimes', 'integer', 'min:0'],
            'configuration.scaling.server.timeout' => ['sometimes', 'integer', 'min:1', 'max:3600'],
            'configuration.observability' => ['sometimes', 'array'],
            'configuration.observability.pulse_ingest_interval' => ['sometimes', 'integer', 'min:1', 'max:3600'],
            'configuration.observability.telescope_ingest_interval' => ['sometimes', 'integer', 'min:1', 'max:3600'],
        ])->validate();

        return $validated['configuration'] ?? [];
    }

    public function configurationForStorage(array $validated, array $current, string $serverId): array
    {
        $scaling = $validated['scaling'] ?? null;
        $observability = $validated['observability'] ?? null;
        unset($validated['scaling'], $validated['observability']);

        $configuration = [
            'max_request_size' => 10000,
            'scaling_enabled' => false,
            'scaling_channel' => 'gateway:reverb:'.$serverId,
            'scaling_server' => null,
            'pulse_ingest_interval' => 15,
            'telescope_ingest_interval' => 15,
            ...$current,
            ...$validated,
        ];
        if (is_array($scaling)) {
            if (array_key_exists('enabled', $scaling)) {
                $configuration['scaling_enabled'] = (bool) $scaling['enabled'];
            }
            if (array_key_exists('channel', $scaling)) {
                $configuration['scaling_channel'] = $scaling['channel'];
            }
            if (array_key_exists('server', $scaling)) {
                $configuration['scaling_server'] = $scaling['server'];
            }
        }
        if (is_array($observability)) {
            foreach (['pulse_ingest_interval', 'telescope_ingest_interval'] as $property) {
                if (array_key_exists($property, $observability)) {
                    $configuration[$property] = $observability[$property];
                }
            }
        }

        return $configuration;
    }

    public function configurationResource(Server $server): array
    {
        $configuration = $server->configuration ?? [];

        return [
            'max_request_size' => (int) ($configuration['max_request_size'] ?? 10000),
            'scaling' => [
                'enabled' => (bool) ($configuration['scaling_enabled'] ?? false),
                'channel' => (string) ($configuration['scaling_channel'] ?? ''),
                'server' => self::safeScalingServer($configuration['scaling_server'] ?? null),
            ],
            'observability' => [
                'pulse_ingest_interval' => (int) ($configuration['pulse_ingest_interval'] ?? 15),
                'telescope_ingest_interval' => (int) ($configuration['telescope_ingest_interval'] ?? 15),
            ],
        ];
    }

    public function validateApplication(array $input, Server $server, ?Application $application): array
    {
        $supported = ['app_id', 'key', 'secret', 'allowed_origins', 'ping_interval', 'activity_timeout', 'max_message_size', 'max_connections', 'accept_client_events_from', 'rate_limiting', 'options'];
        $this->rejectUnsupported($input, $supported, 'application');
        if (isset($input['rate_limiting']) && is_array($input['rate_limiting'])) {
            $this->rejectUnsupported($input['rate_limiting'], ['enabled', 'max_attempts', 'decay_seconds', 'terminate_on_limit'], 'rate_limiting');
        }
        if (isset($input['options']) && is_array($input['options'])) {
            $this->rejectUnsupported($input['options'], ['host', 'port', 'scheme', 'useTLS'], 'options');
        }
        $required = $application === null ? 'required' : 'sometimes';

        return $this->validator->make($input, [
            'app_id' => [$required, 'string', 'max:255', Rule::unique(ServerTables::applications(), 'external_id')->where('server_id', $server->getKey())->ignore($application?->getKey())],
            'key' => [$required, 'string', 'max:255', Rule::unique(ServerTables::applications(), 'key')->where('server_id', $server->getKey())->ignore($application?->getKey())],
            'secret' => [$required, 'string', 'min:16', 'max:4096'],
            'allowed_origins' => [$required, 'array', 'min:1'],
            'allowed_origins.*' => ['string', 'max:253', 'distinct', 'regex:/^(\*|\*\.[A-Za-z0-9.-]+|[A-Za-z0-9.-]+)$/'],
            'ping_interval' => ['sometimes', 'integer', 'min:1', 'max:3600'],
            'activity_timeout' => ['sometimes', 'integer', 'min:1', 'max:3600'],
            'max_message_size' => ['sometimes', 'integer', 'min:1', 'max:104857600'],
            'max_connections' => ['sometimes', 'nullable', 'integer', 'min:1'],
            'accept_client_events_from' => ['sometimes', Rule::in(['all', 'members', 'none'])],
            'rate_limiting' => ['sometimes', 'nullable', 'array'],
            'rate_limiting.enabled' => ['sometimes', 'boolean'],
            'rate_limiting.max_attempts' => ['required_if:rate_limiting.enabled,true', 'integer', 'min:1'],
            'rate_limiting.decay_seconds' => ['required_if:rate_limiting.enabled,true', 'integer', 'min:1'],
            'rate_limiting.terminate_on_limit' => ['sometimes', 'boolean'],
            'options' => ['sometimes', 'nullable', 'array'],
            'options.host' => ['sometimes', 'nullable', 'string', 'max:253'],
            'options.port' => ['sometimes', 'integer', 'min:1', 'max:65535'],
            'options.scheme' => ['sometimes', Rule::in(['http', 'https'])],
            'options.useTLS' => ['sometimes', 'boolean'],
        ])->validate();
    }

    public function applicationForStorage(array $validated, ?Application $application): array
    {
        if ($application === null) {
            $validated = [
                'allowed_origins' => [],
                'ping_interval' => 60,
                'activity_timeout' => 30,
                'max_message_size' => 10000,
                'max_connections' => null,
                'accept_client_events_from' => 'members',
                'rate_limiting' => null,
                'options' => null,
                ...$validated,
            ];
        }

        $storage = [];
        if (array_key_exists('app_id', $validated)) {
            $storage['external_id'] = $validated['app_id'];
            unset($validated['app_id']);
        }
        if (array_key_exists('key', $validated)) {
            $storage['key'] = $validated['key'];
            unset($validated['key']);
        }
        if (array_key_exists('secret', $validated)) {
            $storage['credentials'] = [...($application?->credentials ?? []), 'secret' => $validated['secret']];
            unset($validated['secret']);
        }
        if ($validated !== []) {
            $storage['configuration'] = [...($application?->configuration ?? []), ...$validated];
        }

        return $storage;
    }

    public function applicationResource(Application $application): array
    {
        $configuration = $application->configuration ?? [];

        return [
            'id' => $application->getKey(),
            'server_id' => $application->server_id,
            'app_id' => $application->external_id,
            'key' => $application->key,
            'allowed_origins' => array_values($configuration['allowed_origins'] ?? []),
            'ping_interval' => (int) ($configuration['ping_interval'] ?? 60),
            'activity_timeout' => (int) ($configuration['activity_timeout'] ?? 30),
            'max_message_size' => (int) ($configuration['max_message_size'] ?? 10000),
            'max_connections' => $configuration['max_connections'] ?? null,
            'accept_client_events_from' => (string) ($configuration['accept_client_events_from'] ?? 'members'),
            'rate_limiting' => $configuration['rate_limiting'] ?? null,
            'options' => $configuration['options'] ?? [],
            'revision' => $application->revision,
            'created_at' => $application->created_at?->toAtomString(),
            'updated_at' => $application->updated_at?->toAtomString(),
        ];
    }

    private function rejectUnsupported(array $input, array $supported, string $key): void
    {
        $unsupported = array_values(array_diff(array_keys($input), $supported));
        if ($unsupported !== []) {
            throw ValidationException::withMessages([
                $key => ['Unsupported properties: '.implode(', ', $unsupported).'.'],
            ]);
        }
    }

    private static function safeScalingServer(mixed $server): array
    {
        if (! is_array($server)) {
            return [];
        }
        $passwordConfigured = isset($server['password']) && $server['password'] !== '';
        unset($server['password']);
        if (isset($server['url'])) {
            $server['url_configured'] = $server['url'] !== '';
            unset($server['url']);
        }
        $server['password_configured'] = $passwordConfigured;

        return $server;
    }
}
