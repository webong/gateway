<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Weaver\Planner\WeaverServiceProvider;

// The packaged Spinner/CLI uses only WorkFlow core; Planner is host supplied.
if (class_exists(ExtensionProviderRegistry::class) && class_exists(Illuminate\Support\ServiceProvider::class)) {
    ExtensionProviderRegistry::register(WeaverServiceProvider::class);
}
