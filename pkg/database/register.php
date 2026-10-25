<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Database\DatabaseServiceProvider;

ExtensionProviderRegistry::register(DatabaseServiceProvider::class);
