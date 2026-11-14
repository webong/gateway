<?php

declare(strict_types=1);
require dirname(__DIR__, 2) . '/vendor/autoload.php';
use Webong\Gateway\Weaver\QueueScheduler;
use Webong\Gateway\Weaver\QueueWorker;
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\ValueObjects\FlowStepDefinition;
use Webong\WorkFlow\ValueObjects\FlowStepInvocation;
use Webong\WorkFlow\ValueObjects\StepResult;
[$script, $dsn, $user, $password, $dir, $mode, $name] = $argv;
$pdo = new PDO($dsn, $user, $password);
$queue = new QueueScheduler($pdo);
$deadline = microtime(true) + 15;
while (! file_exists($dir . '/go')) {
    if (microtime(true) > $deadline) { throw new RuntimeException('Start barrier timed out'); }
    usleep(10000);
}
if ($mode === 'crash') {
    [$claim] = $queue->claim(1, 5);
    $queue->beginExecution($claim->claimId);
    $pdo->prepare('INSERT INTO queue_effects VALUES (?)')->execute([$claim->metadata['idempotency_key']]);
    echo json_encode($claim->toArray(), JSON_THROW_ON_ERROR);
    exit(0); // external effect committed, queue completion never written
} elseif ($mode === 'deliver') {
    $claimId = trim(file_get_contents($dir . '/claim-id'));
    $worker = new QueueWorker($queue, [new class($dir) implements FlowStepExecutor {
        public function __construct(private string $dir) {}
        public function supports(FlowStepDefinition $step): bool { return true; }
        public function execute(FlowStepInvocation $invocation): StepResult {
            file_put_contents($this->dir . '/effects', "effect\n", FILE_APPEND | LOCK_EX);
            return StepResult::completed();
        }
    }]);
    echo $worker->handle($claimId) ? 'done' : 'none';
} elseif ($mode === 'claim') {
    $claims = $queue->claim(1, 60);
    echo $claims === [] ? 'none' : $claims[0]->step->id;
} else {
    $worker = new QueueWorker($queue, [new class($dir, $name) implements FlowStepExecutor {
        public function __construct(private string $dir, private string $name) {}
        public function supports(FlowStepDefinition $step): bool { return true; }
        public function execute(FlowStepInvocation $invocation): StepResult {
            touch($this->dir . '/' . $this->name . '.started');
            $other = $this->name === 'one' ? 'two' : 'one';
            $deadline = microtime(true) + 10;
            while (! file_exists($this->dir . '/' . $other . '.started')) {
                if (microtime(true) > $deadline) { throw new RuntimeException('Independent steps did not overlap'); }
                usleep(10000);
            }
            return StepResult::completed();
        }
    }]);
    if (! $worker->tick(60)) { throw new RuntimeException('No work claimed'); }
    echo 'done';
}
