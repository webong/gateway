<?php

declare(strict_types=1);

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;

it('adds ownership fields to an existing generic configuration table without erasing its records', function (): void {
    config()->set('database.connections.upgrade_fixture', [
        'driver' => 'sqlite', 'database' => ':memory:', 'prefix' => '', 'foreign_key_constraints' => true,
    ]);
    DB::setDefaultConnection('upgrade_fixture');
    try {
        Schema::create('configurations', static function (Blueprint $table): void {
            $table->uuid('id')->primary();
            $table->string('kind', 96);
            $table->string('key_hash', 64)->unique();
            $table->text('key');
            $table->longText('document');
            $table->unsignedSmallInteger('schema_version')->default(1);
            $table->boolean('enabled')->default(true);
            $table->unsignedBigInteger('revision')->default(1);
            $table->timestamps();
        });
        DB::table('configurations')->insert([
            'id' => '11111111-1111-4111-8111-111111111111', 'kind' => 'email.runtime-policy',
            'key_hash' => str_repeat('a', 64), 'key' => 'default', 'document' => 'encrypted-existing-document',
            'schema_version' => 1, 'enabled' => true, 'revision' => 1,
            'created_at' => now(), 'updated_at' => now(),
        ]);
        $migration = require dirname(__DIR__, 2).'/pkg/planner/src/database/migrations/2026_10_19_000001_upgrade_configuration_ownership.php';
        $migration->up();
        expect(Schema::hasColumn('configurations', 'owner'))->toBeTrue()
            ->and(Schema::hasColumn('configurations', 'parent_id'))->toBeTrue()
            ->and(Schema::hasTable('resource_observations'))->toBeTrue()
            ->and(DB::table('configurations')->where('id', '11111111-1111-4111-8111-111111111111')->value('document'))
            ->toBe('encrypted-existing-document');
        $migration->up();
        expect(DB::table('configurations')->count())->toBe(1);
    } finally {
        DB::setDefaultConnection('sqlite');
    }
});
