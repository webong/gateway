<?php

declare(strict_types=1);

use Gateway\Router\WorkermanLaravelHandler;
use Workerman\Protocols\Http\Request;

it('handles repeated Workerman requests with isolated Laravel applications', function (): void {
    $testApplication = $this->app;
    $dataDirectory = sys_get_temp_dir().'/gateway-workerman-'.bin2hex(random_bytes(8));
    mkdir($dataDirectory, 0700, true);
    touch($dataDirectory.'/gateway.sqlite');
    putenv('GATEWAY_ROUTER_DATA_DIR='.$dataDirectory);
    putenv('APP_KEY=base64:'.base64_encode(str_repeat('w', 32)));
    putenv('SESSION_DRIVER=array');
    putenv('DB_DATABASE='.$dataDirectory.'/gateway.sqlite');
    putenv('APP_RUNNING_IN_CONSOLE=false');
    require_once dirname(__DIR__, 2).'/app/WorkermanLaravelHandler.php';

    try {
        $handler = new WorkermanLaravelHandler(dirname(__DIR__, 2).'/app');
        $request = new Request("GET /health HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n");

        $first = (string) $handler->handle($request);
        $second = (string) $handler->handle($request);

        expect($first)->toContain('204 No Content')
            ->and($second)->toContain('204 No Content');
    } finally {
        Laravel\Octane\CurrentApplication::set($testApplication);
        Illuminate\Container\Container::setInstance($testApplication);
        Illuminate\Support\Facades\Facade::clearResolvedInstances();
        Illuminate\Support\Facades\Facade::setFacadeApplication($testApplication);
        putenv('GATEWAY_ROUTER_DATA_DIR');
        putenv('APP_KEY');
        putenv('SESSION_DRIVER');
        putenv('DB_DATABASE');
        putenv('APP_RUNNING_IN_CONSOLE');
        foreach (['APP_SERVICES_CACHE', 'APP_PACKAGES_CACHE', 'APP_CONFIG_CACHE', 'APP_ROUTES_CACHE', 'APP_EVENTS_CACHE'] as $key) {
            unset($_ENV[$key], $_SERVER[$key]);
        }
        (new Illuminate\Filesystem\Filesystem())->deleteDirectory($dataDirectory);
    }
});
