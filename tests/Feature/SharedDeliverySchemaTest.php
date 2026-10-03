<?php

declare(strict_types=1);

use Illuminate\Support\Facades\Schema;

it('creates the shared delivery lease schema without a gateway table prefix', function (): void {
    expect(Schema::hasTable('deliveries'))->toBeTrue();
    expect(Schema::hasColumns('deliveries', [
        'id', 'payload', 'state', 'event_id', 'next_attempt_at',
        'lease_owner', 'lease_token', 'lease_until',
        'idempotency_fingerprint', 'idempotency_expires_at',
    ]))->toBeTrue();
});
