<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('configurations', function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('kind', 96)->index();
            $table->string('key_hash', 64)->unique();
            $table->text('key');
            $table->longText('document');
            $table->unsignedSmallInteger('schema_version')->default(1);
            $table->boolean('enabled')->default(true);
            $table->unsignedBigInteger('revision')->default(1);
            $table->timestamps();
        });
        Schema::create('configuration_revisions', function (Blueprint $table): void {
            $table->bigIncrements('id');
            $table->uuid('resource_id');
            $table->string('kind', 96);
            $table->string('key_hash', 64);
            $table->unsignedBigInteger('revision');
            $table->string('operation', 16);
            $table->longText('document');
            $table->boolean('enabled');
            $table->timestamp('created_at');
            $table->unique(['resource_id', 'revision']);
            $table->index(['kind', 'key_hash']);
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('configuration_revisions');
        Schema::dropIfExists('configurations');
    }
};
