<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Models;

use Illuminate\Database\Eloquent\Concerns\HasUuids;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Webong\Gateway\Dns\DnsHookTables;

final class DnsHookEvent extends Model
{
    use HasUuids;

    protected $guarded = [];

    protected $casts = [
        'payload' => 'array',
        'status_code' => 'integer',
        'received_at' => 'immutable_datetime',
    ];

    public function getTable(): string
    {
        return DnsHookTables::events();
    }

    /** @return BelongsTo<DnsHook, $this> */
    public function hook(): BelongsTo
    {
        return $this->belongsTo(DnsHook::class, 'dns_hook_id');
    }
}
