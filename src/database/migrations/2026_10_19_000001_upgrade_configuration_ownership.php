<?php

declare(strict_types=1);

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

/** Additive upgrade for local volumes that ran the first generic-CMS migration. */
return new class extends Migration
{
    public function up(): void
    {
        if (Schema::hasTable('configurations')) {
            if (! Schema::hasColumn('configurations', 'parent_id')) {
                Schema::table('configurations', static function (Blueprint $table): void {
                    $table->uuid('parent_id')->nullable();
                });
            }
            if (! Schema::hasColumn('configurations', 'owner')) {
                Schema::table('configurations', static function (Blueprint $table): void {
                    $table->string('owner', 200)->nullable();
                });
            }
        }
        if (! Schema::hasTable('resource_observations')) {
            Schema::create('resource_observations', static function (Blueprint $table): void {
                $table->uuid('resource_id')->primary();
                $table->string('state', 32)->default('untested');
                $table->unsignedBigInteger('observed_revision')->nullable();
                $table->timestamp('observed_at')->nullable();
                $table->timestamps();
                $table->foreign('resource_id')->references('id')->on('configurations')->cascadeOnDelete();
            });
        }
    }

    public function down(): void
    {
        // Do not strip owner or parent links from a database that may now use them.
    }
};
