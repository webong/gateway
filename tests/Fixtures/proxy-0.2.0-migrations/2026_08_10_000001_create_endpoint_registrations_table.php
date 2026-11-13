<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration {
    public function up(): void
    {
        Schema::create((string) config('proxy.tables.endpoint_registrations', 'endpoint_registrations'), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('owner_id');
            $table->uuid('endpoint_id')->nullable();
            $table->string('endpoint_key', 64);
            $table->string('client');
            $table->string('registry');
            $table->string('registry_id')->nullable();
            $table->text('callback_url')->nullable();
            $table->json('metadata')->nullable();
            $table->boolean('is_active')->default(true);
            $table->timestamps();

            $table->unique(['owner_id', 'endpoint_key'], 'endpoint_registrations_owner_key_unique');
            $table->index(['endpoint_key'], 'endpoint_registrations_key_index');
            $table->index(['endpoint_id'], 'endpoint_registrations_endpoint_index');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists((string) config('proxy.tables.endpoint_registrations', 'endpoint_registrations'));
    }
};
