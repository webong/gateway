<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb;

use Illuminate\Support\Collection;
use Laravel\Reverb\Application;
use Laravel\Reverb\Contracts\ApplicationProvider;
use Laravel\Reverb\Exceptions\InvalidApplication;
use Webong\Gateway\Reverb\Models\ReverbApplication;

final readonly class GatewayApplicationProvider implements ApplicationProvider
{
    public function __construct(private ReverbServerContext $server)
    {
    }

    /** @return Collection<int, Application> */
    public function all(): Collection
    {
        return ReverbApplication::query()
            ->where('server_id', $this->server->id())
            ->orderBy('id')
            ->get()
            ->map(fn (ReverbApplication $application): Application => $this->toReverb($application));
    }

    public function findById(string $id): Application
    {
        return $this->find('external_id', $id);
    }

    public function findByKey(string $key): Application
    {
        return $this->find('key', $key);
    }

    private function find(string $column, string $value): Application
    {
        $application = ReverbApplication::query()
            ->where('server_id', $this->server->id())
            ->where($column, $value)
            ->first();

        if ($application === null) {
            throw new InvalidApplication;
        }

        return $this->toReverb($application);
    }

    private function toReverb(ReverbApplication $application): Application
    {
        return new Application(
            id: $application->app_id,
            key: $application->key,
            secret: $application->secret,
            pingInterval: $application->ping_interval,
            activityTimeout: $application->activity_timeout,
            allowedOrigins: $application->allowed_origins,
            maxMessageSize: $application->max_message_size,
            maxConnections: $application->max_connections,
            acceptClientEventsFrom: $application->accept_client_events_from,
            rateLimiting: $application->rate_limiting,
            options: $application->options ?? [],
        );
    }
}
