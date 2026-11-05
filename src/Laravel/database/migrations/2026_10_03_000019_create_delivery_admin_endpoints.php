<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('delivery_admin_endpoints', function (Blueprint $table): void {
            $table->char('id', 32)->primary();
            $table->string('origin', 2048);
            $table->unsignedBigInteger('expires_unix');
            $table->char('signature', 64);

            $table->index('expires_unix');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('delivery_admin_endpoints');
    }
};
