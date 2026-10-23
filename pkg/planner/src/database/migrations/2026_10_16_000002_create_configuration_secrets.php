<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        // Write-only live credentials are not copied into configuration_revisions.
        Schema::create('configuration_secrets', function (Blueprint $table): void {
            $table->uuid('resource_id')->primary();
            $table->longText('ciphertext');
            $table->unsignedBigInteger('version')->default(1);
            $table->timestamps();
            $table->foreign('resource_id')->references('id')->on('configurations')->cascadeOnDelete();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('configuration_secrets');
    }
};
