<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Shell\ShellExtensionServiceProvider;

ExtensionProviderRegistry::register(ShellExtensionServiceProvider::class);
