<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Dns\DnsServiceProvider;

ExtensionProviderRegistry::register(DnsServiceProvider::class);
