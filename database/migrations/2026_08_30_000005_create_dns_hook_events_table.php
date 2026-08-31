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
        Schema::create(DnsHookTables::events(), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->foreignUuid('dns_hook_id')->constrained(DnsHookTables::hooks())->cascadeOnDelete();
            $table->string('event_id')->unique();
            $table->string('query_name');
            $table->string('query_type', 16);
            $table->string('query_class', 16);
            $table->text('data')->nullable();
            $table->string('source_address')->nullable();
            $table->string('transport', 8)->nullable();
            $table->string('decision_action', 16)->nullable();
            $table->unsignedSmallInteger('status_code')->nullable();
            $table->text('error')->nullable();
            $table->json('payload')->nullable();
            $table->timestamp('received_at');
            $table->timestamps();

            $table->index(['dns_hook_id', 'received_at']);
            $table->index(['dns_hook_id', 'query_type', 'received_at']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists(DnsHookTables::events());
    }
};
