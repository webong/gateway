<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\JavaScript\JavaScriptExtensionServiceProvider;

ExtensionProviderRegistry::register(JavaScriptExtensionServiceProvider::class);
