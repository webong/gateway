<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration {
    public function up(): void
    {
        Schema::create((string) config('proxy.tables.profiles', 'profiles'), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->uuid('endpoint_id');
            $table->string('name', 64);
            $table->string('driver', 64);
            $table->text('configuration');
            $table->boolean('is_active')->default(true);
            $table->timestamps();

            $table->unique(['endpoint_id', 'name'], 'profiles_endpoint_name_unique');
            $table->index(['endpoint_id', 'driver', 'is_active'], 'profiles_endpoint_driver_active_index');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists((string) config('proxy.tables.profiles', 'profiles'));
    }
};
