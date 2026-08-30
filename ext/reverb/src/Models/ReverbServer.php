<?php

declare(strict_types=1);

namespace Webong\Gateway\Reverb\Models;

use Illuminate\Database\Eloquent\Builder;
use Illuminate\Database\Eloquent\Relations\HasMany;
use Webong\Gateway\Servers\Models\Server;

final class ReverbServer extends Server
{
    protected static function booted(): void
    {
        static::addGlobalScope('reverb', fn (Builder $query) => $query->where($query->qualifyColumn('type'), 'reverb'));
        static::creating(function (ReverbServer $server): void {
            $server->type = 'reverb';
        });
    }

    public function applications(): HasMany
    {
        return $this->hasMany(ReverbApplication::class, 'server_id');
    }

}
