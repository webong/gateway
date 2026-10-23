<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Reverb\ReverbExtensionServiceProvider;

ExtensionProviderRegistry::register(ReverbExtensionServiceProvider::class);
