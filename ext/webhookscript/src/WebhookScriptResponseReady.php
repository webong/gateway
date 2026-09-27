<?php

declare(strict_types=1);

namespace Webong\Gateway\WebhookScript;

use RuntimeException;

/** @internal Normal control flow used by WebhookScript respond() and stop(). */
final class WebhookScriptResponseReady extends RuntimeException
{
}
