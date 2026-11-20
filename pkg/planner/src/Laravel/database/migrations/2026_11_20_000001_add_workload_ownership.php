<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Laravel\Servers\ServerTables;

return new class extends Migration
{
    public function up(): void
    {
        Schema::table(ServerTables::nodes(), function (Blueprint $table): void {
            $table->string('owner_id', 32)->nullable();
        });
        Schema::table(ServerTables::instances(), function (Blueprint $table): void {
            $table->json('endpoints')->nullable();
        });
    }

    public function down(): void
    {
        Schema::table(ServerTables::instances(), function (Blueprint $table): void {
            $table->dropColumn('endpoints');
        });
        Schema::table(ServerTables::nodes(), function (Blueprint $table): void {
            $table->dropColumn('owner_id');
        });
    }
};
