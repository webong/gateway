<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb\Tests\Fixtures;

use Illuminate\Database\Migrations\Migrator;
use Illuminate\Support\ServiceProvider;
use ReflectionClass;
use Webong\Gateway\GatewayServiceProvider;

final class ReverbSmokeServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        if (getenv('GATEWAY_REVERB_SMOKE') !== '1' || getenv('GATEWAY_REVERB_SERVER_ID')) {
            return;
        }

        /** @var Migrator $migrator */
        $migrator = $this->app->make('migrator');
        if (! $migrator->repositoryExists()) {
            $migrator->getRepository()->createRepository();
        }

        $provider = new ReflectionClass(GatewayServiceProvider::class);
        $migrator->run(dirname((string) $provider->getFileName(), 2).'/database/migrations');
    }
}
