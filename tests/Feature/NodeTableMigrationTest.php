<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Schema;
use Webong\Gateway\Servers\ServerTables;

it('creates the canonical nodes table with session and target fields', function (): void {
    expect(ServerTables::nodes())->toBe('nodes')
        ->and(Schema::hasTable('nodes'))->toBeTrue()
        ->and(Schema::hasColumn('nodes', 'session_id'))->toBeTrue()
        ->and(Schema::hasColumn('nodes', 'target_id'))->toBeTrue();
});
