<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver;

use Webong\WorkFlow\ValueObjects\FlowStateSubject;

/** One unambiguous private storage/execution identity, preserving the caller's logical run ID. */
final class RpcRunIdentity
{
    public static function executionId(FlowStateSubject $subject, string $flowKey, string $runId): string
    {
        $identity = '';
        foreach ([$subject->type, $subject->id, $flowKey, $runId] as $component) {
            $identity .= strlen($component).':'.$component;
        }
        return 'workflow-rpc:'.hash('sha256', $identity);
    }
}
