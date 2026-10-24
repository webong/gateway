<?php

declare(strict_types=1);

namespace Webong\Gateway\Contracts;

use Webong\Gateway\Protocols\CompanionActionInvocation;
use Webong\Gateway\Protocols\CompanionActionResult;

/** Trusted, operator-installed PHP behavior invoked by Spinner. */
interface CompanionAction
{
    public function handle(CompanionActionInvocation $invocation): CompanionActionResult;
}
