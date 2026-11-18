<?php

declare(strict_types=1);

use Phinx\Migration\AbstractMigration;

final class CreateExtensionProbe extends AbstractMigration
{
    public function change(): void
    {
        if (! $this->hasTable('accounts')) {
            throw new RuntimeException('The core schema must precede its extension.');
        }
        $this->table('extension_probes')->addColumn('value', 'string')->create();
    }
}
