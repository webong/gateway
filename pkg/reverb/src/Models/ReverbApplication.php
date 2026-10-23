<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb\Models;

use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Webong\Gateway\Servers\Models\Application;

final class ReverbApplication extends Application
{
    public function getAppIdAttribute(): string
    {
        return (string) $this->external_id;
    }

    public function getSecretAttribute(): string
    {
        return (string) ($this->credentials['secret'] ?? '');
    }

    /** @return list<string> */
    public function getAllowedOriginsAttribute(): array
    {
        return array_values($this->configuration['allowed_origins'] ?? []);
    }

    public function getPingIntervalAttribute(): int
    {
        return (int) ($this->configuration['ping_interval'] ?? 60);
    }

    public function getActivityTimeoutAttribute(): int
    {
        return (int) ($this->configuration['activity_timeout'] ?? 30);
    }

    public function getMaxMessageSizeAttribute(): int
    {
        return (int) ($this->configuration['max_message_size'] ?? 10000);
    }

    public function getMaxConnectionsAttribute(): ?int
    {
        $connections = $this->configuration['max_connections'] ?? null;

        return $connections === null ? null : (int) $connections;
    }

    public function getAcceptClientEventsFromAttribute(): string
    {
        return (string) ($this->configuration['accept_client_events_from'] ?? 'members');
    }

    /** @return array<string, mixed>|null */
    public function getRateLimitingAttribute(): ?array
    {
        $limiting = $this->configuration['rate_limiting'] ?? null;

        return is_array($limiting) ? $limiting : null;
    }

    /** @return array<string, mixed>|null */
    public function getOptionsAttribute(): ?array
    {
        $options = $this->configuration['options'] ?? null;

        return is_array($options) ? $options : null;
    }

    public function server(): BelongsTo
    {
        return $this->belongsTo(ReverbServer::class, 'server_id');
    }
}
