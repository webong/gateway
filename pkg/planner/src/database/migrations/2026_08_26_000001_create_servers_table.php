<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Servers\ServerTables;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create(ServerTables::servers(), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('type');
            $table->string('name')->unique();
            $table->string('hostname');
            $table->string('path')->default('');
            $table->string('driver')->default('local');
            $table->string('desired_state')->default('stopped');
            $table->unsignedSmallInteger('replicas')->default(1);
            $table->unsignedBigInteger('revision')->default(1);
            $table->text('configuration')->nullable();
            $table->timestamps();

            $table->unique(['hostname', 'path']);
            $table->index('desired_state');
            $table->index('type');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists(ServerTables::servers());
    }
};
