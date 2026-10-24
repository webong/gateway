<?php

declare(strict_types=1);

use Webong\Gateway\Centrifugo\CentrifugoIntegrationServiceProvider;
use Webong\Gateway\Contracts\ExtensionProviderRegistry;

ExtensionProviderRegistry::register(CentrifugoIntegrationServiceProvider::class);
