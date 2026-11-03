<?php

declare(strict_types=1);

use Phinx\Db\Table;
use Phinx\Migration\AbstractMigration;

final class CreateGatewayPlannerSchema extends AbstractMigration
{
    public function change(): void
    {
        $tables = array_merge([
            'servers' => 'servers',
            'applications' => 'applications',
            'instances' => 'instances',
            'nodes' => 'nodes',
            'targets' => 'targets',
            'endpoints' => 'endpoints',
        ], (array) ($this->getAdapter()->getOption('gateway_tables') ?? []));

        $this->createAccounts();
        $this->createTargets($tables['targets']);
        $this->createServers($tables['servers'], $tables['targets']);
        $this->createApplications($tables['applications'], $tables['servers']);
        $this->createInstances($tables['instances'], $tables['servers']);
        $this->createNodes($tables['nodes'], $tables['targets']);
        $this->createEvents($tables['endpoints']);
        $this->createSchedules();
        $this->createOperationalTables();
        $this->createConfigurationTables();
        $this->createCompanionExecutions();
    }

    private function createAccounts(): void
    {
        $table = $this->table('accounts', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'string', ['limit' => 200])
            ->addColumn('token_hash', 'string', ['limit' => 64]);
        $this->timestamps($table)
            ->addIndex(['token_hash'], ['unique' => true, 'name' => 'accounts_token_hash_unique'])
            ->create();
    }

    private function createTargets(string $targets): void
    {
        $table = $this->table($targets, ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('name', 'string')
            ->addColumn('driver', 'string')
            ->addColumn('connection', 'text')
            ->addColumn('worker_token_hash', 'string', ['limit' => 64])
            ->addIndex(['name'], ['unique' => true]);
        $this->timestamps($table)->create();
    }

    private function createServers(string $servers, string $targets): void
    {
        $table = $this->table($servers, ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('type', 'string')
            ->addColumn('name', 'string')
            ->addColumn('hostname', 'string')
            ->addColumn('path', 'string', ['default' => ''])
            ->addColumn('driver', 'string', ['default' => 'local'])
            ->addColumn('desired_state', 'string', ['default' => 'stopped'])
            ->addColumn('replicas', 'smallinteger', ['signed' => false, 'default' => 1])
            ->addColumn('revision', 'biginteger', ['signed' => false, 'default' => 1])
            ->addColumn('configuration', 'text', ['null' => true])
            ->addColumn('target_id', 'uuid', ['null' => true])
            ->addIndex(['name'], ['unique' => true])
            ->addIndex(['hostname', 'path'], ['unique' => true])
            ->addIndex(['desired_state'])
            ->addIndex(['type'])
            ->addForeignKey('target_id', $targets, 'id', ['delete' => 'RESTRICT']);
        $this->timestamps($table)->create();
    }

    private function createApplications(string $applications, string $servers): void
    {
        $table = $this->table($applications, ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('server_id', 'uuid')
            ->addColumn('external_id', 'string')
            ->addColumn('key', 'string', ['null' => true])
            ->addColumn('credentials', 'text', ['null' => true])
            ->addColumn('configuration', 'text', ['null' => true])
            ->addColumn('revision', 'biginteger', ['signed' => false, 'default' => 1])
            ->addIndex(['server_id', 'external_id'], ['unique' => true])
            ->addIndex(['server_id', 'key'], ['unique' => true])
            ->addForeignKey('server_id', $servers, 'id', ['delete' => 'CASCADE']);
        $this->timestamps($table)->create();
    }

    private function createInstances(string $instances, string $servers): void
    {
        $table = $this->table($instances, ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('server_id', 'uuid')
            ->addColumn('node_id', 'string')
            ->addColumn('runtime', 'string', ['default' => 'local'])
            ->addColumn('pid', 'biginteger', ['signed' => false, 'null' => true])
            ->addColumn('host', 'string', ['null' => true])
            ->addColumn('port', 'integer', ['signed' => false, 'null' => true])
            ->addColumn('state', 'string')
            ->addColumn('revision', 'biginteger', ['signed' => false])
            ->addColumn('error', 'text', ['null' => true])
            ->addColumn('started_at', 'timestamp', ['null' => true])
            ->addColumn('heartbeat_at', 'timestamp', ['null' => true])
            ->addColumn('stopped_at', 'timestamp', ['null' => true])
            ->addIndex(['server_id', 'state'])
            ->addIndex(['node_id', 'state'])
            ->addForeignKey('server_id', $servers, 'id', ['delete' => 'CASCADE']);
        $this->timestamps($table)->create();
    }

    private function createNodes(string $nodes, string $targets): void
    {
        $table = $this->table($nodes, ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'string')
            ->addColumn('drivers', 'json')
            ->addColumn('session_id', 'string', ['limit' => 32, 'null' => true])
            ->addColumn('heartbeat_at', 'timestamp')
            ->addColumn('target_id', 'uuid', ['null' => true])
            ->addIndex(['heartbeat_at'])
            ->addForeignKey('target_id', $targets, 'id', ['delete' => 'RESTRICT']);
        $this->timestamps($table)->create();
    }

    private function createEvents(string $endpoints): void
    {
        $table = $this->table('events', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('endpoint_id', 'uuid', ['null' => true])
            ->addColumn('subject_kind', 'string', ['limit' => 96, 'null' => true])
            ->addColumn('subject_id', 'string', ['limit' => 200, 'null' => true])
            ->addColumn('event_id', 'string')
            ->addColumn('protocol', 'string', ['limit' => 32])
            ->addColumn('kind', 'string', ['limit' => 32])
            ->addColumn('source_address', 'string', ['null' => true])
            ->addColumn('transport', 'string', ['limit' => 16, 'null' => true])
            ->addColumn('status_code', 'smallinteger', ['signed' => false, 'null' => true])
            ->addColumn('decision_action', 'string', ['limit' => 16, 'null' => true])
            ->addColumn('error', 'text', ['null' => true])
            ->addColumn('attributes', 'json', ['null' => true])
            ->addColumn('payload', 'json', ['null' => true])
            ->addColumn('received_at', 'timestamp')
            ->addIndex(['event_id'], ['unique' => true])
            ->addIndex(['endpoint_id', 'received_at'])
            ->addIndex(['endpoint_id', 'protocol', 'kind', 'received_at'])
            ->addIndex(['subject_kind', 'subject_id', 'received_at'])
            ->addIndex(['received_at'], ['name' => 'events_received_at_index']);
        $this->timestamps($table);
        // The endpoint table is supplied by webong/proxy and may be migrated separately.
        if ($this->getAdapter()->hasTable($endpoints)) {
            $table->addForeignKey('endpoint_id', $endpoints, 'id', ['delete' => 'CASCADE']);
        }
        $table->create();
    }

    private function createSchedules(): void
    {
        $schedules = $this->table('schedules', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('origin', 'string', ['null' => true])
            ->addColumn('key', 'string', ['null' => true])
            ->addColumn('name', 'string')
            ->addColumn('cron', 'string')
            ->addColumn('timezone', 'string')
            ->addColumn('language', 'string')
            ->addColumn('source', 'text')
            ->addColumn('payload', 'json')
            ->addColumn('enabled', 'boolean', ['default' => true])
            ->addColumn('next_run_at', 'timestamp', ['null' => true])
            ->addIndex(['next_run_at'])
            ->addIndex(['origin', 'key'], ['unique' => true]);
        $this->timestamps($schedules)->create();

        $runs = $this->table('schedule_runs', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('schedule_id', 'uuid')
            ->addColumn('scheduled_for', 'timestamp')
            ->addColumn('kind', 'string', ['default' => 'cron'])
            ->addColumn('occurrence_key', 'string', ['null' => true])
            ->addColumn('status', 'string')
            ->addColumn('claim_token', 'uuid')
            ->addColumn('lease_until', 'timestamp')
            ->addColumn('language', 'string')
            ->addColumn('source', 'text')
            ->addColumn('payload', 'json')
            ->addColumn('deliveries_queued', 'integer', ['signed' => false, 'default' => 0])
            ->addColumn('error', 'text', ['null' => true])
            ->addColumn('completed_at', 'timestamp', ['null' => true])
            ->addIndex(['schedule_id', 'occurrence_key'], ['unique' => true])
            ->addIndex(['status', 'lease_until'])
            ->addForeignKey('schedule_id', 'schedules', 'id', ['delete' => 'CASCADE']);
        $this->timestamps($runs)->create();

        $notifications = $this->table('schedule_notifications', ['id' => false, 'primary_key' => ['run_id']])
            ->addColumn('run_id', 'uuid')
            ->addColumn('state', 'string')
            ->addColumn('attempts', 'integer', ['signed' => false, 'default' => 0])
            ->addColumn('next_attempt_at', 'timestamp')
            ->addColumn('lease_until', 'timestamp', ['null' => true])
            ->addColumn('claim_token', 'uuid', ['null' => true])
            ->addColumn('last_outcome', 'string', ['null' => true])
            ->addColumn('notified_at', 'timestamp', ['null' => true])
            ->addIndex(['next_attempt_at'])
            ->addIndex(['state', 'next_attempt_at'])
            ->addForeignKey('run_id', 'schedule_runs', 'id', ['delete' => 'CASCADE']);
        $this->timestamps($notifications)->create();
    }

    private function createOperationalTables(): void
    {
        $usage = $this->table('usage_counters', ['id' => false, 'primary_key' => ['metric', 'subject_hash', 'day']])
            ->addColumn('metric', 'string', ['limit' => 96])
            ->addColumn('subject_hash', 'char', ['limit' => 64])
            ->addColumn('day', 'date')
            ->addColumn('attempts', 'integer', ['signed' => false, 'default' => 0])
            ->addColumn('bytes', 'biginteger', ['signed' => false, 'default' => 0])
            ->addIndex(['day']);
        $this->timestamps($usage)->create();

        $deliveries = $this->table('deliveries', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'char', ['limit' => 32])
            ->addColumn('payload', 'json')
            ->addColumn('state', 'string', ['limit' => 16])
            ->addColumn('event_id', 'string', ['null' => true])
            ->addColumn('next_attempt_at', 'timestamp', ['timezone' => true])
            ->addColumn('lease_owner', 'char', ['limit' => 32, 'null' => true])
            ->addColumn('lease_token', 'biginteger', ['signed' => false, 'default' => 0])
            ->addColumn('lease_until', 'timestamp', ['timezone' => true, 'null' => true])
            ->addColumn('idempotency_fingerprint', 'char', ['limit' => 64, 'null' => true])
            ->addColumn('idempotency_expires_at', 'timestamp', ['timezone' => true, 'null' => true])
            ->addIndex(['state', 'next_attempt_at', 'lease_until'])
            ->addIndex(['event_id', 'updated_at']);
        $this->timestamps($deliveries, true)->create();

        $this->table('gateway_cache', ['id' => false, 'primary_key' => ['key']])
            ->addColumn('key', 'string')->addColumn('value', 'text')->addColumn('expiration', 'integer')->create();
        $this->table('gateway_cache_locks', ['id' => false, 'primary_key' => ['key']])
            ->addColumn('key', 'string')->addColumn('owner', 'string')->addColumn('expiration', 'integer')->create();
        $this->table('delivery_admin_endpoints', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'char', ['limit' => 32])
            ->addColumn('origin', 'string', ['limit' => 2048])
            ->addColumn('expires_unix', 'biginteger', ['signed' => false])
            ->addColumn('signature', 'char', ['limit' => 64])
            ->addIndex(['expires_unix'])->create();
    }

    private function createConfigurationTables(): void
    {
        $configurations = $this->table('configurations', ['id' => false, 'primary_key' => ['id']])
            ->addColumn('id', 'uuid')
            ->addColumn('kind', 'string', ['limit' => 96])
            ->addColumn('parent_id', 'uuid', ['null' => true])
            ->addColumn('owner', 'string', ['limit' => 200, 'null' => true])
            ->addColumn('key_hash', 'string', ['limit' => 64])
            ->addColumn('key', 'text')
            ->addColumn('document', 'text')
            ->addColumn('schema_version', 'smallinteger', ['signed' => false, 'default' => 1])
            ->addColumn('enabled', 'boolean', ['default' => true])
            ->addColumn('revision', 'biginteger', ['signed' => false, 'default' => 1])
            ->addIndex(['kind'])
            ->addIndex(['key_hash'], ['unique' => true])
            ->addIndex(['kind', 'parent_id'])
            ->addIndex(['kind', 'owner'])
            ->addForeignKey('owner', 'accounts', 'id');
        $this->timestamps($configurations)->create();

        $this->table('configuration_revisions')
            ->addColumn('resource_id', 'uuid')
            ->addColumn('kind', 'string', ['limit' => 96])
            ->addColumn('key_hash', 'string', ['limit' => 64])
            ->addColumn('revision', 'biginteger', ['signed' => false])
            ->addColumn('operation', 'string', ['limit' => 16])
            ->addColumn('document', 'text')
            ->addColumn('enabled', 'boolean')
            ->addColumn('created_at', 'timestamp')
            ->addIndex(['resource_id', 'revision'], ['unique' => true])
            ->addIndex(['kind', 'key_hash'])->create();

        $observations = $this->table('resource_observations', ['id' => false, 'primary_key' => ['resource_id']])
            ->addColumn('resource_id', 'uuid')
            ->addColumn('state', 'string', ['limit' => 32, 'default' => 'untested'])
            ->addColumn('observed_revision', 'biginteger', ['signed' => false, 'null' => true])
            ->addColumn('observed_at', 'timestamp', ['null' => true])
            ->addForeignKey('resource_id', 'configurations', 'id', ['delete' => 'CASCADE']);
        $this->timestamps($observations)->create();

        $secrets = $this->table('configuration_secrets', ['id' => false, 'primary_key' => ['resource_id']])
            ->addColumn('resource_id', 'uuid')
            ->addColumn('ciphertext', 'text')
            ->addColumn('version', 'biginteger', ['signed' => false, 'default' => 1])
            ->addForeignKey('resource_id', 'configurations', 'id', ['delete' => 'CASCADE']);
        $this->timestamps($secrets)->create();

        $checkpoints = $this->table('resource_checkpoints', ['id' => false, 'primary_key' => ['resource_id']])
            ->addColumn('resource_id', 'uuid')
            ->addColumn('cursor', 'json')
            ->addColumn('lease_token', 'uuid', ['null' => true])
            ->addColumn('lease_until', 'timestamp', ['null' => true])
            ->addIndex(['lease_until'])
            ->addForeignKey('resource_id', 'configurations', 'id', ['delete' => 'CASCADE']);
        $this->timestamps($checkpoints)->create();
    }

    private function createCompanionExecutions(): void
    {
        $table = $this->table('companion_action_executions', ['id' => false, 'primary_key' => ['execution_id']])
            ->addColumn('execution_id', 'string', ['limit' => 128])
            ->addColumn('action', 'string', ['limit' => 160])
            ->addColumn('companion_version', 'string', ['limit' => 64, 'null' => true])
            ->addColumn('companion_digest', 'char', ['limit' => 64, 'null' => true])
            ->addColumn('idempotency_hash', 'char', ['limit' => 64])
            ->addColumn('request_hash', 'char', ['limit' => 64])
            ->addColumn('status', 'string', ['limit' => 16])
            ->addColumn('attempts', 'integer', ['signed' => false, 'default' => 1])
            ->addColumn('claim_token', 'char', ['limit' => 32, 'null' => true])
            ->addColumn('lease_expires_at', 'timestamp', ['timezone' => true, 'null' => true])
            ->addColumn('result', 'text', ['null' => true])
            ->addColumn('started_at', 'timestamp', ['timezone' => true])
            ->addColumn('completed_at', 'timestamp', ['timezone' => true, 'null' => true])
            ->addIndex(['action', 'idempotency_hash'], ['unique' => true])
            ->addIndex(['action', 'status', 'started_at'])
            ->addIndex(['status', 'lease_expires_at']);
        $this->timestamps($table, true)->create();
    }

    private function timestamps(Table $table, bool $withTimezone = false): Table
    {
        return $table
            ->addColumn('created_at', 'timestamp', ['timezone' => $withTimezone, 'null' => true])
            ->addColumn('updated_at', 'timestamp', ['timezone' => $withTimezone, 'null' => true]);
    }
}
