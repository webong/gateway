<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Servers\ServerTables;

return new class extends Migration
{
    public function up(): void
    {
        $tableName = ServerTables::nodes();
        if (Schema::hasTable($tableName)) {
            return;
        }

        // The former migration filename may already be recorded in a host
        // application's migrations table. Preserve its rows when the new
        // migration name is applied on upgrade.
        if ($tableName === 'nodes' && Schema::hasTable('gateway_nodes')) {
            Schema::rename('gateway_nodes', $tableName);

            return;
        }

        Schema::create($tableName, function (Blueprint $table): void {
            $table->string('id')->primary();
            $table->json('drivers');
            $table->timestamp('heartbeat_at');
            $table->timestamps();

            $table->index('heartbeat_at');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists(ServerTables::nodes());
    }
};
