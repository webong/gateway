<?php

declare(strict_types=1);

use Phinx\Migration\AbstractMigration;

final class AddWorkloadOwnership extends AbstractMigration
{
    public function change(): void
    {
        $tables = (array) ($this->getAdapter()->getOption('gateway_tables') ?? []);
        $nodes = $this->table($tables['nodes'] ?? 'nodes');
        if (! $nodes->hasColumn('owner_id')) {
            $nodes->addColumn('owner_id', 'string', ['limit' => 32, 'null' => true])->update();
        }
        $instances = $this->table($tables['instances'] ?? 'instances');
        if (! $instances->hasColumn('endpoints')) {
            $instances->addColumn('endpoints', 'json', ['null' => true])->update();
        }
    }
}
