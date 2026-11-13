<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration {
    public function up(): void
    {
        Schema::create((string) config('proxy.tables.endpoints', 'endpoints'), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('client');
            $table->string('external_id');
            $table->string('endpoint_key', 64)->unique();
            $table->text('signing_secret')->nullable();
            $table->text('verification_token')->nullable();
            $table->string('credential_owner_id')->nullable();
            $table->jsonb('metadata')->nullable();
            $table->boolean('is_managed')->default(false);
            $table->boolean('is_active')->default(true);
            $table->timestamps();

            $table->unique(['client', 'external_id']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists((string) config('proxy.tables.endpoints', 'endpoints'));
    }
};
