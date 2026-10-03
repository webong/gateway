<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Route;
use Webong\Gateway\PlannerReadiness;

Route::get('/health', static fn () => response()->noContent());
Route::get('/ready', static fn (PlannerReadiness $readiness) => response()->noContent($readiness->ready() ? 204 : 503));
