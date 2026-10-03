<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('email_domains', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('domain', 253)->unique();
            $table->string('verification_token', 64);
            $table->timestamp('verified_at')->nullable();
            $table->boolean('enabled')->default(false);
            $table->timestamps();
        });

        Schema::create('email_aliases', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->uuid('domain_id');
            $table->string('local_part', 64);
            $table->json('recipients');
            $table->boolean('enabled')->default(true);
            $table->timestamps();

            $table->foreign('domain_id')->references('id')->on('email_domains')->cascadeOnDelete();
            $table->unique(['domain_id', 'local_part']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('email_aliases');
        Schema::dropIfExists('email_domains');
    }
};
