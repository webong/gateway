<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('email_outbound_receipt_owners', function (Blueprint $table): void {
            $table->char('receipt_id', 32)->primary();
            $table->char('identity_hash', 64);
            $table->string('node', 64);
            $table->timestamp('expires_at');
            $table->index(['identity_hash', 'expires_at']);
        });
        Schema::create('email_outbound_rate_usage', function (Blueprint $table): void {
            $table->uuid('transport_id');
            $table->unsignedBigInteger('minute');
            $table->unsignedInteger('attempts')->default(0);
            $table->primary(['transport_id', 'minute']);
        });
        Schema::create('email_outbound_rate_reservations', function (Blueprint $table): void {
            $table->char('delivery_id', 64);
            $table->unsignedInteger('attempt_index');
            $table->uuid('transport_id');
            $table->unsignedBigInteger('minute');
            $table->primary(['delivery_id', 'attempt_index', 'transport_id']);
            $table->index(['transport_id', 'minute']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('email_outbound_receipt_owners');
        Schema::dropIfExists('email_outbound_rate_reservations');
        Schema::dropIfExists('email_outbound_rate_usage');
    }
};
