<?php

declare(strict_types=1);

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Servers\ServerTables;

it('uses nodes by default and preserves an existing gateway_nodes table', function (): void {
    expect(ServerTables::nodes())->toBe('nodes')
        ->and(Schema::hasTable('nodes'))->toBeTrue();

    Schema::drop('nodes');
    Schema::create('gateway_nodes', function (Blueprint $table): void {
        $table->string('id')->primary();
        $table->json('drivers');
        $table->timestamp('heartbeat_at');
        $table->timestamps();
    });
    DB::table('gateway_nodes')->insert([
        'id' => 'node-a',
        'drivers' => '[]',
        'heartbeat_at' => now(),
        'created_at' => now(),
        'updated_at' => now(),
    ]);

    $migration = require __DIR__.'/../../database/migrations/2026_09_21_000005_create_nodes_table.php';
    $migration->up();

    expect(Schema::hasTable('gateway_nodes'))->toBeFalse()
        ->and(Schema::hasTable('nodes'))->toBeTrue()
        ->and(DB::table('nodes')->where('id', 'node-a')->exists())->toBeTrue();
});
