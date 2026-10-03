<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocol\GatewayEvent;

/**
 * Deployment-owned credential authority for TLS-gated SMTP submission.
 *
 * Return the canonical account identity and exact envelope sender addresses
 * granted to this login, or null to deny it. Implementations must verify the
 * AUTH credential in the event and must not forward it to registry
 * subscriptions or other untrusted targets.
 */
interface EmailSubmissionAuthenticator
{
    public function authorize(GatewayEvent $event): ?EmailSubmissionGrant;
}
