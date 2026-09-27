<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\WebhookScript\WebhookScriptExtensionServiceProvider;

ExtensionProviderRegistry::register(WebhookScriptExtensionServiceProvider::class);
