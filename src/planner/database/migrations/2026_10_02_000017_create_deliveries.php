<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('deliveries', function (Blueprint $table): void {
            $table->char('id', 32)->primary();
            $table->json('payload');
            $table->string('state', 16);
            $table->string('event_id')->nullable();
            $table->timestampTz('next_attempt_at');
            $table->char('lease_owner', 32)->nullable();
            $table->unsignedBigInteger('lease_token')->default(0);
            $table->timestampTz('lease_until')->nullable();
            $table->char('idempotency_fingerprint', 64)->nullable();
            $table->timestampTz('idempotency_expires_at')->nullable();
            $table->timestampsTz();

            $table->index(['state', 'next_attempt_at', 'lease_until']);
            $table->index(['event_id', 'updated_at']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('deliveries');
    }
};
