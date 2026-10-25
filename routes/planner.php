<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Route;
use Webong\Gateway\Events\Http\EndpointEventController;
use Webong\Gateway\Events\Http\WebEventPolicyController;
use Webong\Gateway\Webstream\Http\WebstreamPrivateSourceController;
use Webong\Gateway\Webstream\Http\WebstreamRoutePolicyController;

Route::get('/web/event-policy', [WebEventPolicyController::class, 'show']);
Route::post('/web/event-policy', [WebEventPolicyController::class, 'store']);
Route::put('/web/event-policy', [WebEventPolicyController::class, 'replace']);
Route::delete('/web/event-policy', [WebEventPolicyController::class, 'destroy']);

Route::get('/webstream/private-sources', [WebstreamPrivateSourceController::class, 'index']);
Route::post('/webstream/private-sources', [WebstreamPrivateSourceController::class, 'store']);
Route::get('/webstream/private-sources/{source}', [WebstreamPrivateSourceController::class, 'show']);
Route::put('/webstream/private-sources/{source}', [WebstreamPrivateSourceController::class, 'replace']);
Route::delete('/webstream/private-sources/{source}', [WebstreamPrivateSourceController::class, 'destroy']);
Route::get('/webstream/routes', [WebstreamRoutePolicyController::class, 'index']);
Route::post('/webstream/routes', [WebstreamRoutePolicyController::class, 'store']);
Route::get('/webstream/routes/{policy}', [WebstreamRoutePolicyController::class, 'show']);
Route::put('/webstream/routes/{policy}', [WebstreamRoutePolicyController::class, 'replace']);
Route::delete('/webstream/routes/{policy}', [WebstreamRoutePolicyController::class, 'destroy']);

Route::get('/registry/endpoints/{endpointKey}/events', EndpointEventController::class)
    ->name('registry.endpoints.events');
