<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('email_domain_submission_usage', function (Blueprint $table): void {
            $table->char('domain_hash', 64);
            $table->date('day');
            $table->unsignedInteger('attempts')->default(0);
            $table->unsignedBigInteger('bytes')->default(0);
            $table->timestamps();

            $table->primary(['domain_hash', 'day']);
            $table->index('day');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('email_domain_submission_usage');
    }
};
