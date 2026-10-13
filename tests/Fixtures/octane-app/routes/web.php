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

Route::match(['GET', 'POST'], '/http-smoke/stream', static function (Request $request) {
    if ($request->isMethod('POST')) {
        return response()->stream(static function () use ($request): void {
            echo "data: body=".$request->getContent()."\n\n";
            if (ob_get_level() > 0) { ob_flush(); }
            flush();
            usleep(200000);
            echo "data: done\n\n";
        }, 200, ['Content-Type' => 'text/event-stream']);
    }
    return response("id: event-43\nevent: smoke\ndata: resumed=".$request->header('Last-Event-ID', '')."\n\n", 200, [
        'Content-Type' => 'text/event-stream',
        'Cache-Control' => 'no-cache',
    ]);
})->withoutMiddleware([ValidateCsrfToken::class]);
