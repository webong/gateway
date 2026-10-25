<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Route;
use Webong\Gateway\Dns\Http\DnsEndpointController;
use Webong\Gateway\Dns\Http\DnsForwarderController;
use Webong\Gateway\Dns\Http\DnsObservationController;
use Webong\Gateway\Dns\Http\DnsZoneController;

Route::get('/dns/forwarders', [DnsForwarderController::class, 'index'])->name('dns.forwarders.index');
Route::post('/dns/forwarders', [DnsForwarderController::class, 'store'])->name('dns.forwarders.store');
Route::get('/dns/forwarders/{forwarder}', [DnsForwarderController::class, 'show'])->name('dns.forwarders.show');
Route::put('/dns/forwarders/{forwarder}', [DnsForwarderController::class, 'replace'])->name('dns.forwarders.replace');
Route::delete('/dns/forwarders/{forwarder}', [DnsForwarderController::class, 'destroy'])->name('dns.forwarders.destroy');
Route::get('/dns/zones', [DnsZoneController::class, 'index'])->name('dns.zones.index');
Route::post('/dns/zones', [DnsZoneController::class, 'store'])->name('dns.zones.store');
Route::get('/dns/zones/{zone}', [DnsZoneController::class, 'show'])->name('dns.zones.show');
Route::put('/dns/zones/{zone}/records', [DnsZoneController::class, 'replaceRecords'])->name('dns.zones.records');
Route::post('/dns/zones/{zone}/provision', [DnsZoneController::class, 'provision'])->name('dns.zones.provision');
Route::delete('/dns/zones/{zone}', [DnsZoneController::class, 'destroy'])->name('dns.zones.destroy');
Route::get('/dns/endpoints', [DnsEndpointController::class, 'index'])->name('dns.endpoints.index');
Route::post('/dns/endpoints', [DnsEndpointController::class, 'store'])->name('dns.endpoints.store');
Route::get('/dns/endpoints/{endpoint}', [DnsEndpointController::class, 'show'])->name('dns.endpoints.show');
Route::patch('/dns/endpoints/{endpoint}', [DnsEndpointController::class, 'update'])->name('dns.endpoints.update');
Route::delete('/dns/endpoints/{endpoint}', [DnsEndpointController::class, 'destroy'])->name('dns.endpoints.destroy');
Route::get('/dns/{subject}/{resource}/events', [DnsObservationController::class, 'events'])
    ->whereIn('subject', ['zones', 'endpoints', 'forwarders'])->name('dns.observations.events');
Route::get('/dns/{subject}/{resource}/usage', [DnsObservationController::class, 'usage'])
    ->whereIn('subject', ['zones', 'endpoints', 'forwarders'])->name('dns.observations.usage');
