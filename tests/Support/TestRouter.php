<?php

declare(strict_types=1);

namespace Webong\WebRelay\Tests\Support;

use Webong\WebProxy\Contracts\Router;
use Webong\WebProxy\Models\WebProxyCall;

final class TestRouter implements Router
{
    public function routes(WebProxyCall $webhookCall): iterable
    {
        return [];
    }
}
