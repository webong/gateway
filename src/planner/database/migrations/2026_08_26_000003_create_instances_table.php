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
        Schema::create(ServerTables::instances(), function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->foreignUuid('server_id')->constrained(ServerTables::servers())->cascadeOnDelete();
            $table->string('node_id');
            $table->string('runtime')->default('local');
            $table->unsignedBigInteger('pid')->nullable();
            $table->string('host')->nullable();
            $table->unsignedInteger('port')->nullable();
            $table->string('state');
            $table->unsignedBigInteger('revision');
            $table->text('error')->nullable();
            $table->timestamp('started_at')->nullable();
            $table->timestamp('heartbeat_at')->nullable();
            $table->timestamp('stopped_at')->nullable();
            $table->timestamps();

            $table->index(['server_id', 'state']);
            $table->index(['node_id', 'state']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists(ServerTables::instances());
    }
};
