<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Fixtures;

use RuntimeException;
use Webong\Gateway\Email\DmarcAssessment;
use Webong\Gateway\Email\EmailMessagePolicy;
use Webong\Gateway\Email\EmailPolicyVerdict;
use Webong\Gateway\Protocols\GatewayEvent;

/** Test-only policy that exercises SMTP reject and temporary defer paths. */
final class EmailSmokeMessagePolicy implements EmailMessagePolicy
{
    public function assess(GatewayEvent $event, array $recipients, ?DmarcAssessment $authentication): EmailPolicyVerdict
    {
        if (str_contains($event->payload, 'Subject: policy-block')) {
            return EmailPolicyVerdict::Reject;
        }
        if (str_contains($event->payload, 'Subject: policy-defer')) {
            return EmailPolicyVerdict::Defer;
        }
        if (str_contains($event->payload, 'Subject: policy-error')) {
            throw new RuntimeException('The test scanner is unavailable.');
        }

        return EmailPolicyVerdict::Accept;
    }
}
