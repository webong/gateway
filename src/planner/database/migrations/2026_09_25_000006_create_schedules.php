<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('schedules', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('name');
            $table->string('cron');
            $table->string('timezone');
            $table->string('language');
            $table->text('source');
            $table->json('payload');
            $table->boolean('enabled')->default(true);
            $table->timestamp('next_run_at')->nullable()->index();
            $table->timestamps();
        });

        Schema::create('schedule_runs', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->uuid('schedule_id');
            $table->timestamp('scheduled_for');
            $table->string('status');
            $table->uuid('claim_token');
            $table->timestamp('lease_until');
            $table->string('language');
            $table->text('source');
            $table->json('payload');
            $table->unsignedInteger('deliveries_queued')->default(0);
            $table->text('error')->nullable();
            $table->timestamp('completed_at')->nullable();
            $table->timestamps();

            $table->foreign('schedule_id')->references('id')->on('schedules')->cascadeOnDelete();
            $table->unique(['schedule_id', 'scheduled_for']);
            $table->index(['status', 'lease_until']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('schedule_runs');
        Schema::dropIfExists('schedules');
    }
};
