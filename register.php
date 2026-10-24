<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Mercure\MercureIntegrationServiceProvider;

ExtensionProviderRegistry::register(MercureIntegrationServiceProvider::class);
