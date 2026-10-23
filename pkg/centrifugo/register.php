<?php

declare(strict_types=1);

use Webong\Gateway\Centrifugo\CentrifugoExtensionServiceProvider;
use Webong\Gateway\Contracts\ExtensionProviderRegistry;

ExtensionProviderRegistry::register(CentrifugoExtensionServiceProvider::class);
