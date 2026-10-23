<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('schedule_notifications', function (Blueprint $table): void {
            $table->uuid('run_id')->primary();
            $table->string('state');
            $table->unsignedInteger('attempts')->default(0);
            $table->timestamp('next_attempt_at')->index();
            $table->timestamp('lease_until')->nullable();
            $table->uuid('claim_token')->nullable();
            $table->string('last_outcome')->nullable();
            $table->timestamp('notified_at')->nullable();
            $table->timestamps();

            $table->foreign('run_id')->references('id')->on('schedule_runs')->cascadeOnDelete();
            $table->index(['state', 'next_attempt_at']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('schedule_notifications');
    }
};
