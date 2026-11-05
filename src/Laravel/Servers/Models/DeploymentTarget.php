<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Servers\Models;

use Illuminate\Database\Eloquent\Concerns\HasUuids;
use Illuminate\Database\Eloquent\Model;
use Webong\Gateway\Laravel\Servers\ServerTables;

final class DeploymentTarget extends Model
{
    use HasUuids;

    protected $guarded = [];

    protected $hidden = ['worker_token_hash', 'connection'];

    public function getTable(): string
    {
        return ServerTables::targets();
    }

    protected function casts(): array
    {
        return ['connection' => 'encrypted:array'];
    }
}
