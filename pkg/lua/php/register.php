<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Lua\LuaExtensionServiceProvider;

ExtensionProviderRegistry::register(LuaExtensionServiceProvider::class);
