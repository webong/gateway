<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Protocol\GatewayEvent;

/** A deployment-trusted scanner or abuse policy for a managed SMTP message. */
interface EmailMessagePolicy
{
    /**
     * @param list<string> $recipients Resolved forwarding destinations.
     */
    public function assess(GatewayEvent $event, array $recipients, ?DmarcAssessment $authentication): EmailPolicyVerdict;
}
