<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        // A foreign key must use its core-owned identity column's collation.
        $identity = collect(Schema::getColumns('configurations'))->firstWhere('name', 'id');
        $collation = in_array(Schema::getConnection()->getDriverName(), ['mysql', 'mariadb'], true)
            ? ($identity['collation'] ?? null) : null;
        Schema::create('email_delivery_status_events', function (Blueprint $table) use ($collation): void {
            $table->char('delivery_id', 32)->primary();
            $table->char('event_id', 64)->nullable()->unique();
            $table->string('account_id', 200);
            $table->uuid('hook_id')->collation($collation);
            $table->string('node', 64);
            $table->string('state', 16)->default('watching');
            $table->string('delivery_state', 16)->nullable();
            $table->unsignedInteger('delivery_attempts')->nullable();
            $table->timestamp('occurred_at')->nullable();
            $table->string('reason_code', 64)->nullable();
            $table->unsignedInteger('callback_attempts')->default(0);
            $table->timestamp('next_attempt_at')->index();
            $table->timestamp('lease_until')->nullable();
            $table->uuid('claim_token')->nullable();
            $table->timestamp('notified_at')->nullable();
            $table->timestamps();

            $table->index(['state', 'next_attempt_at']);
            $table->foreign('hook_id')->references('id')->on('configurations')->cascadeOnDelete();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('email_delivery_status_events');
    }
};
