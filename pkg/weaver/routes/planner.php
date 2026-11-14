<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Route;
use Webong\Gateway\Weaver\Planner\Http\WeaverController;

Route::get('/weaver/operations', [WeaverController::class, 'operations']);
Route::prefix('/weaver/accounts/{account}')->group(static function(): void {
    Route::get('/definitions', [WeaverController::class, 'definitions']);
    Route::post('/definitions', [WeaverController::class, 'publish']);
    Route::get('/grants', [WeaverController::class, 'grants']);
    Route::post('/grants', [WeaverController::class, 'createGrant']);
    Route::put('/grants/{grant}', [WeaverController::class, 'replaceGrant']);
    Route::delete('/grants/{grant}', [WeaverController::class, 'revokeGrant']);
});
