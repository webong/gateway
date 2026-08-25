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

Route::get('/http-smoke/worker', static fn () => response('http-worker'));

Route::post('/http-smoke/pass-through', static function (Request $request) {
    return response($request->getContent());
})->withoutMiddleware([
    ValidateCsrfToken::class,
]);

Route::post('/http-smoke/receiver', static function (Request $request) {
    $recordPath = getenv('GATEWAY_HTTP_RECEIVER_FILE');

    if (is_string($recordPath) && $recordPath !== '') {
        file_put_contents($recordPath, $request->getContent()."\n".(string) $request->header('X-Webhook-Forwarder-Delivery-Id')."\n", LOCK_EX);
    }

    return response()->noContent();
})->withoutMiddleware([
    ValidateCsrfToken::class,
]);
