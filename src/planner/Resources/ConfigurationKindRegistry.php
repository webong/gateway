<?php

declare(strict_types=1);

namespace Webong\Gateway\Resources;

/** Publicly manageable document kinds; new kinds must be registered deliberately. */
final class ConfigurationKindRegistry
{
    /** @return list<string> */
    public function all(): array
    {
        return ['webstream.private-source', 'webstream.route-policy', 'web.event-policy',
            'schedule.notification', 'shell.hook-grant', 'email.submission-policy',
            'email.runtime-policy', 'email.srs-policy', 'email.upstream',
            'email.hosted-adapter', 'email.hosted-route', 'email.dkim-key'];
    }

    public function contains(string $kind): bool
    {
        return in_array($kind, $this->all(), true);
    }
}
