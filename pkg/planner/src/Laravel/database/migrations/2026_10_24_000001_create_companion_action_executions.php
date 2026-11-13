<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('companion_action_executions', function (Blueprint $table): void {
            $table->string('execution_id', 128)->primary();
            $table->string('action', 160);
            $table->string('companion_version', 64)->nullable();
            $table->char('companion_digest', 64)->nullable();
            $table->char('idempotency_hash', 64);
            $table->char('request_hash', 64);
            $table->string('status', 16);
            $table->unsignedInteger('attempts')->default(1);
            $table->char('claim_token', 32)->nullable();
            $table->timestampTz('lease_expires_at')->nullable();
            // Results may contain application data and are encrypted by Planner.
            $table->text('result')->nullable();
            $table->timestampTz('started_at');
            $table->timestampTz('completed_at')->nullable();
            $table->timestampsTz();

            $table->unique(['action', 'idempotency_hash']);
            $table->index(['action', 'status', 'started_at']);
            $table->index(['status', 'lease_expires_at']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('companion_action_executions');
    }
};
