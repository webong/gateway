<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Contracts;

use Webong\Gateway\Servers\Models\Server;

interface ServerType
{
    public function type(): string;

    public function defaultPath(): string;

    /**
     * Validate public configuration and return its validated public shape.
     *
     * @param  array<string, mixed>  $configuration
     * @return array<string, mixed>
     */
    public function validateConfiguration(array $configuration, ?Server $server, int $replicas): array;

    /**
     * Convert validated public configuration to the encrypted storage shape.
     *
     * @param  array<string, mixed>  $validated
     * @param  array<string, mixed>  $current
     * @return array<string, mixed>
     */
    public function configurationForStorage(array $validated, array $current, string $serverId): array;

    /** @return array<string, mixed> */
    public function configurationResource(Server $server): array;

    public function supportsApplications(): bool;
}
