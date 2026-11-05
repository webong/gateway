<?php

declare(strict_types=1);

use Symfony\Component\Routing\Route;
use Symfony\Component\Routing\RouteCollection;
use Webong\Gateway\Symfony\Http\PlanningController;

$routes = new RouteCollection();
foreach (['plan', 'event'] as $operation) {
    $routes->add('gateway_'.$operation, new Route(
        '/_internal/gateway/'.$operation,
        ['_controller' => PlanningController::class.'::'.$operation],
        methods: ['POST'],
    ));
}

return $routes;
