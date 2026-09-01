<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('events', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->foreignUuid('endpoint_id')->constrained('web_proxy_endpoints')->cascadeOnDelete();
            $table->string('event_id')->unique();
            $table->string('protocol', 32);
            $table->string('kind', 32);
            $table->string('source_address')->nullable();
            $table->string('transport', 16)->nullable();
            $table->unsignedSmallInteger('status_code')->nullable();
            $table->string('decision_action', 16)->nullable();
            $table->text('error')->nullable();
            $table->json('attributes')->nullable();
            $table->json('payload')->nullable();
            $table->timestamp('received_at');
            $table->timestamps();

            $table->index(['endpoint_id', 'received_at']);
            $table->index(['endpoint_id', 'protocol', 'kind', 'received_at']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('events');
    }
};
