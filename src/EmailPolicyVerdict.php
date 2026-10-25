<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

enum EmailPolicyVerdict
{
    case Accept;
    case Reject;
    case Defer;
}
