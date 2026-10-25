<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Web\WebServiceProvider;

ExtensionProviderRegistry::register(WebServiceProvider::class);
