<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Route;
use Webong\Gateway\Dns\Http\DnsHookController;
use Webong\Gateway\Dns\Http\DnsHookEventController;
use Webong\Gateway\Dns\Http\DnsHookUsageController;
use Webong\Gateway\Dns\Http\DnsZoneController;
use Webong\Gateway\Dns\Http\InternalDnsZoneController;

Route::get('/_internal/gateway/dns/zones', InternalDnsZoneController::class);
Route::get('/dns/zones', [DnsZoneController::class, 'index'])->name('dns.zones.index');
Route::post('/dns/zones', [DnsZoneController::class, 'store'])->name('dns.zones.store');
Route::get('/dns/zones/{zone}', [DnsZoneController::class, 'show'])->name('dns.zones.show');
Route::put('/dns/zones/{zone}/records', [DnsZoneController::class, 'replaceRecords'])->name('dns.zones.records');
Route::post('/dns/zones/{zone}/provision', [DnsZoneController::class, 'provision'])->name('dns.zones.provision');
Route::delete('/dns/zones/{zone}', [DnsZoneController::class, 'destroy'])->name('dns.zones.destroy');
Route::get('/dns/hooks', [DnsHookController::class, 'index'])->name('dns.hooks.index');
Route::post('/dns/hooks', [DnsHookController::class, 'store'])->name('dns.hooks.store');
Route::get('/dns/hooks/{hook}', [DnsHookController::class, 'show'])->name('dns.hooks.show');
Route::patch('/dns/hooks/{hook}', [DnsHookController::class, 'update'])->name('dns.hooks.update');
Route::delete('/dns/hooks/{hook}', [DnsHookController::class, 'destroy'])->name('dns.hooks.destroy');
Route::get('/dns/hooks/{hook}/events', DnsHookEventController::class)->name('dns.hooks.events');
Route::get('/dns/hooks/{hook}/usage', DnsHookUsageController::class)->name('dns.hooks.usage');
