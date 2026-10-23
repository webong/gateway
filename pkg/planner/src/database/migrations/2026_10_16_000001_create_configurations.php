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
            $table->uuid('parent_id')->nullable();
            $table->string('owner', 200)->nullable();
            $table->string('key_hash', 64)->unique();
            $table->text('key');
            $table->longText('document');
            $table->unsignedSmallInteger('schema_version')->default(1);
            $table->boolean('enabled')->default(true);
            $table->unsignedBigInteger('revision')->default(1);
            $table->timestamps();
            $table->index(['kind', 'parent_id']);
            $table->index(['kind', 'owner']);
            $table->foreign('owner')->references('id')->on('accounts');
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
        Schema::create('resource_observations', function (Blueprint $table): void {
            $table->uuid('resource_id')->primary();
            $table->string('state', 32)->default('untested');
            $table->unsignedBigInteger('observed_revision')->nullable();
            $table->timestamp('observed_at')->nullable();
            $table->timestamps();
            $table->foreign('resource_id')->references('id')->on('configurations')->cascadeOnDelete();
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('resource_observations');
        Schema::dropIfExists('configuration_revisions');
        Schema::dropIfExists('configurations');
    }
};
