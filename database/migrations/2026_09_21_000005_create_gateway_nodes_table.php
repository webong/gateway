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
        Schema::create(ServerTables::nodes(), function (Blueprint $table): void {
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
