<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Dns\DnsHookTables;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create(DnsHookTables::hooks(), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('external_id')->unique();
            $table->string('name')->nullable();
            $table->string('token', 63)->unique();
            $table->string('endpoint_key', 64)->unique();
            $table->boolean('is_active')->default(true);
            $table->json('metadata')->nullable();
            $table->unsignedBigInteger('query_count')->default(0);
            $table->timestamp('last_queried_at')->nullable();
            $table->timestamps();
            $table->softDeletes();

            $table->index(['is_active', 'created_at']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists(DnsHookTables::hooks());
    }
};
