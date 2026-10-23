<?php

declare(strict_types=1);

namespace Webong\Gateway\HookScript;

use RuntimeException;

/** @internal Normal control flow used by HookScript respond() and stop(). */
final class HookScriptResponseReady extends RuntimeException
{
}
