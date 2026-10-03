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
        Schema::table(ServerTables::nodes(), function (Blueprint $table): void {
            $table->string('session_id', 32)->nullable();
        });
    }

    public function down(): void
    {
        Schema::table(ServerTables::nodes(), function (Blueprint $table): void {
            $table->dropColumn('session_id');
        });
    }
};
