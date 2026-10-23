<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Models;

use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Webong\Gateway\Servers\ServerTables;

class Instance extends Model
{
    public $incrementing = false;

    protected $keyType = 'string';

    protected $guarded = [];

    public function getTable(): string
    {
        return ServerTables::instances();
    }

    protected function casts(): array
    {
        return [
            'pid' => 'integer',
            'port' => 'integer',
            'revision' => 'integer',
            'started_at' => 'immutable_datetime',
            'heartbeat_at' => 'immutable_datetime',
            'stopped_at' => 'immutable_datetime',
        ];
    }

    public function server(): BelongsTo
    {
        return $this->belongsTo(Server::class, 'server_id');
    }
}
