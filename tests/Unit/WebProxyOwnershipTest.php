<?php

declare(strict_types=1);

use Webong\Gateway\Web\WebhookRouter;
use Webong\Gateway\Web\WebServiceProvider;
use Webong\Proxy\WebProxy;
use Webong\Proxy\WebProxyServiceProvider;

it('reserves the Gateway webhook client in Web with one Proxy provider', function (): void {
    expect(config('proxy.routers.gateway'))->toBe(WebhookRouter::class)
        ->and(app(WebProxy::class)->resolve('gateway'))->toBeInstanceOf(WebhookRouter::class)
        ->and(app()->getProviders(WebProxyServiceProvider::class))->toHaveCount(1)
        ->and(app()->getProviders(WebServiceProvider::class))->toHaveCount(1);
});

it('activates Web webhook defaults without replacing host router configuration', function (): void {
    $outer = \Illuminate\Container\Container::getInstance();
    $application = new \Illuminate\Foundation\Application(dirname(__DIR__, 2));
    \Illuminate\Container\Container::setInstance($outer);
    $application->instance('config', new \Illuminate\Config\Repository(['proxy' => [
        'routers' => ['gateway' => Webong\Gateway\Tests\Support\TestRouter::class],
        'channels' => [['name' => 'operator', 'driver' => 'webhook', 'registries' => ['custom']]],
    ]]));
    $application->register(WebServiceProvider::class);
    expect($application['config']->get('proxy.routers.gateway'))->toBe(Webong\Gateway\Tests\Support\TestRouter::class)
        ->and($application['config']->get('proxy.channels.0.name'))->toBe('operator');
});

it('keeps bundled Planner host configuration free of webhook wiring', function (): void {
    $configuration = require dirname(__DIR__, 2).'/app/planner/config.php';
    expect($configuration['proxy'])->not->toHaveKeys(['channels', 'routers']);
});
