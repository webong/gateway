<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Models;

use Illuminate\Database\Eloquent\Concerns\HasUuids;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\Eloquent\Relations\HasMany;
use Webong\Gateway\Servers\ServerTables;

class Server extends Model
{
    use HasUuids;

    protected $guarded = [];

    public function getTable(): string
    {
        return ServerTables::servers();
    }

    protected function casts(): array
    {
        return [
            'replicas' => 'integer',
            'revision' => 'integer',
            'configuration' => 'encrypted:array',
        ];
    }

    public function applications(): HasMany
    {
        return $this->hasMany(Application::class, 'server_id');
    }

    public function instances(): HasMany
    {
        return $this->hasMany(Instance::class, 'server_id');
    }
}
