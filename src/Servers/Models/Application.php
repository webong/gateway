<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Models;

use Illuminate\Database\Eloquent\Concerns\HasUuids;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Webong\Gateway\Servers\ServerTables;

class Application extends Model
{
    use HasUuids;

    protected $guarded = [];

    protected $hidden = ['credentials'];

    public function getTable(): string
    {
        return ServerTables::applications();
    }

    protected function casts(): array
    {
        return [
            'credentials' => 'encrypted:array',
            'configuration' => 'encrypted:array',
            'revision' => 'integer',
        ];
    }

    public function server(): BelongsTo
    {
        return $this->belongsTo(Server::class, 'server_id');
    }
}
