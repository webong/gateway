<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\PHP\PHPExtensionServiceProvider;

ExtensionProviderRegistry::register(PHPExtensionServiceProvider::class);