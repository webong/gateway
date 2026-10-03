<?php

declare(strict_types=1);

it('keeps the internal readiness route private and fails closed when the endpoint table is unavailable', function (): void {
    $this->get('/_internal/gateway/ready')->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'wrong-token')
        ->get('/_internal/gateway/ready')->assertNotFound();
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->get('/_internal/gateway/ready')->assertNoContent();

    config()->set('proxy.tables.endpoints', 'missing_endpoint_table');
    $this->withHeader('X-Gateway-Internal', 'internal-secret')
        ->get('/_internal/gateway/ready')->assertStatus(503);
});
