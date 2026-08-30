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
        Schema::create(ServerTables::applications(), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->foreignUuid('server_id')->constrained(ServerTables::servers())->cascadeOnDelete();
            $table->string('external_id');
            $table->string('key')->nullable();
            $table->text('credentials')->nullable();
            $table->text('configuration')->nullable();
            $table->unsignedBigInteger('revision')->default(1);
            $table->timestamps();

            $table->unique(['server_id', 'external_id']);
            $table->unique(['server_id', 'key']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists(ServerTables::applications());
    }
};
