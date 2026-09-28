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
        Schema::create(ServerTables::targets(), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('name')->unique();
            $table->string('driver');
            $table->text('connection');
            $table->string('worker_token_hash', 64);
            $table->timestamps();
        });
        foreach ([ServerTables::servers(), ServerTables::nodes()] as $name) {
            Schema::table($name, function (Blueprint $table): void {
                $table->foreignUuid('target_id')->nullable()->constrained(ServerTables::targets())->restrictOnDelete();
            });
        }
    }

    public function down(): void
    {
        foreach ([ServerTables::nodes(), ServerTables::servers()] as $name) {
            Schema::table($name, fn (Blueprint $table) => $table->dropConstrainedForeignId('target_id'));
        }
        Schema::dropIfExists(ServerTables::targets());
    }
};
