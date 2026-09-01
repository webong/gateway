<?php

declare(strict_types=1);

namespace Webong\Gateway\Events\Models;

use Illuminate\Database\Eloquent\Concerns\HasUuids;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Webong\WebProxy\Models\WebProxyEndpoint;

final class GatewayEndpointEvent extends Model
{
    use HasUuids;

    protected $guarded = [];

    protected $table = 'events';

    protected $casts = [
        'attributes' => 'array',
        'payload' => 'array',
        'status_code' => 'integer',
        'received_at' => 'immutable_datetime',
    ];

    /** @return BelongsTo<WebProxyEndpoint, $this> */
    public function endpoint(): BelongsTo
    {
        return $this->belongsTo(WebProxyEndpoint::class, 'endpoint_id');
    }
}
