<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Servers\Models;

use Illuminate\Database\Eloquent\Model;
use Webong\Gateway\Laravel\Servers\ServerTables;

final class ProvisioningNode extends Model
{
    public $incrementing = false;

    protected $keyType = 'string';

    protected $guarded = [];

    public function getTable(): string
    {
        return ServerTables::nodes();
    }

    protected function casts(): array
    {
        return [
            'drivers' => 'array',
            'heartbeat_at' => 'immutable_datetime',
        ];
    }
}
