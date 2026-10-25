<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Email\EmailServiceProvider;

ExtensionProviderRegistry::register(EmailServiceProvider::class);
