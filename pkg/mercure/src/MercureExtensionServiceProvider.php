<?php

declare(strict_types=1);

namespace Webong\Gateway\Mercure;

use Illuminate\Support\ServiceProvider;
use Webong\Gateway\Mercure\ServerTypes\MercureServerType;
use Webong\Gateway\Servers\ServerTypeRegistry;

final class MercureExtensionServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->app->singleton(MercureServerType::class);
        $this->app->afterResolving(ServerTypeRegistry::class, function (ServerTypeRegistry $types): void {
            $types->register($this->app->make(MercureServerType::class));
        });
    }
}
