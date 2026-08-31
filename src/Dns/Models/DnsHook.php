<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Models;

use Illuminate\Database\Eloquent\Builder;
use Illuminate\Database\Eloquent\Concerns\HasUuids;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\HasMany;
use Illuminate\Database\Eloquent\SoftDeletes;
use Webong\Gateway\Dns\DnsHookTables;

final class DnsHook extends Model
{
    use HasUuids;
    use SoftDeletes;

    protected $guarded = [];

    protected $casts = [
        'is_active' => 'boolean',
        'metadata' => 'array',
        'query_count' => 'integer',
        'last_queried_at' => 'immutable_datetime',
    ];

    public function getTable(): string
    {
        return DnsHookTables::hooks();
    }

    /** @return HasMany<DnsHookEvent, $this> */
    public function events(): HasMany
    {
        return $this->hasMany(DnsHookEvent::class);
    }

    /** @param Builder<DnsHook> $query */
    public function scopeActive(Builder $query): void
    {
        $query->where('is_active', true);
    }
}
