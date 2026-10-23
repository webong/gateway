<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\HookScript\HookScriptExtensionServiceProvider;

ExtensionProviderRegistry::register(HookScriptExtensionServiceProvider::class);