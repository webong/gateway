<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration {
    public function up(): void
    {
        Schema::create((string) config('proxy.tables.subscriptions', 'subscriptions'), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->uuid('endpoint_id');
            $table->string('owner_id');
            $table->string('registration_id');
            $table->string('webhook_group');
            $table->string('routing_scope');
            $table->string('routing_key');
            $table->string('target_type')->default('request');
            $table->text('target');
            $table->jsonb('metadata')->nullable();
            $table->boolean('is_active')->default(true);
            $table->timestamp('last_delivered_at')->nullable();
            $table->timestamp('last_failed_at')->nullable();
            $table->text('last_error')->nullable();
            $table->timestamps();

            $table->unique(
                ['endpoint_id', 'owner_id', 'registration_id', 'webhook_group', 'routing_scope', 'routing_key', 'target_type'],
                'subscriptions_registration_unique',
            );
            $table->index(
                ['endpoint_id', 'routing_scope', 'routing_key', 'is_active'],
                'subscriptions_routing_index',
            );
        });
    }

    public function down(): void
    {
        Schema::dropIfExists((string) config('proxy.tables.subscriptions', 'subscriptions'));
    }
};
