<?php

declare(strict_types=1);

use Webong\Gateway\Protocol\Response;
use Webong\Gateway\Protocol\RoutePlan;

it('serializes empty response headers as a JSON map for Go', function (): void {
    $json = json_encode(
        RoutePlan::respond(new Response(statusCode: 204))->toArray(),
        JSON_THROW_ON_ERROR,
    );
    $decoded = json_decode($json, false, 512, JSON_THROW_ON_ERROR);

    expect($decoded->immediate_response->headers)->toBeObject()
        ->and((array) $decoded->immediate_response->headers)->toBe([]);
});
