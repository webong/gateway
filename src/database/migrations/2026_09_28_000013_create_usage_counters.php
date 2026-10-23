<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('usage_counters', function (Blueprint $table): void {
            $table->string('metric', 96);
            $table->char('subject_hash', 64);
            $table->date('day');
            $table->unsignedInteger('attempts')->default(0);
            $table->unsignedBigInteger('bytes')->default(0);
            $table->timestamps();

            $table->primary(['metric', 'subject_hash', 'day']);
            $table->index('day');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('usage_counters');
    }
};
