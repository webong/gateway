<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\ZorviaMailbox\ZorviaMailboxExtensionServiceProvider;

ExtensionProviderRegistry::register(ZorviaMailboxExtensionServiceProvider::class);
