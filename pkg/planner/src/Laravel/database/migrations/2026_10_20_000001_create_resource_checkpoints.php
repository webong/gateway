<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('resource_checkpoints', function (Blueprint $table): void {
            $table->uuid('resource_id')->primary();
            $table->json('cursor');
            $table->uuid('lease_token')->nullable();
            $table->timestamp('lease_until')->nullable();
            $table->timestamps();
            $table->foreign('resource_id')->references('id')->on('configurations')->cascadeOnDelete();
            $table->index('lease_until');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('resource_checkpoints');
    }
};
