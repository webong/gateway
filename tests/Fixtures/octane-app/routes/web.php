<?php

declare(strict_types=1);

use Illuminate\Foundation\Http\Middleware\ValidateCsrfToken;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Route;

Route::get('/octane-smoke/worker', static fn () => response('octane-worker'));

Route::post('/octane-smoke/pass-through', static function (Request $request) {
    return response('octane-pass-through:'.$request->getContent());
})->withoutMiddleware([
    ValidateCsrfToken::class,
]);
