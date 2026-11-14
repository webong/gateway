<?php

declare(strict_types=1);

namespace Webong\Gateway\Weaver\Tests\App;

use InvalidArgumentException;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use Webong\Gateway\Weaver\FlowRpcApplication;
use Webong\Gateway\Weaver\RpcFailure;
use Webong\WorkFlow\Contracts\FlowDefinitionProvider;
use Webong\WorkFlow\Contracts\FlowOperationProvider;
use Webong\WorkFlow\Contracts\FlowStateStoreFactory;
use Webong\WorkFlow\Contracts\FlowStepExecutor;
use Webong\WorkFlow\Contracts\ForgettableFlowStateStore;
use Webong\WorkFlow\Services\FlowExecutionDispatcher;
use Webong\WorkFlow\ValueObjects\FlowDefinition;
use Webong\WorkFlow\ValueObjects\FlowOperation;
use Webong\WorkFlow\ValueObjects\FlowStateSubject;
use Webong\WorkFlow\ValueObjects\FlowStepDefinition;
use Webong\WorkFlow\ValueObjects\FlowStepInvocation;
use Webong\WorkFlow\ValueObjects\StepResult;

/**
 * `flow.validate` is a pure function over the request and the host's bound
 * executors: it must work for a document the server has never seen and must
 * never load, execute, or persist anything.
 */
final class FlowRpcValidateTest extends TestCase
{
    public function testItValidatesADocumentTheServerHasNeverRegistered(): void
    {
        // The definition provider and store factory both throw: reaching
        // either proves the method is not pure.
        $result = $this->application()->invoke('flow.validate', [
            'definition' => $this->document(),
        ]);

        self::assertTrue($result['valid']);
        self::assertSame(
            ['capabilities_unchecked', 'operations_unchecked'],
            array_column($result['diagnostics'], 'code'),
        );
    }

    public function testItChecksCapabilitiesFromHostConfiguration(): void
    {
        $result = $this->application($this->provider(), ['approval'])->invoke('flow.validate', [
            'definition' => $this->document(),
        ]);

        self::assertTrue($result['valid']);
        self::assertSame([], $result['diagnostics']);
    }

    public function testItReportsMissingCapabilitiesAsErrors(): void
    {
        $result = $this->application($this->provider(), ['something-else'])->invoke('flow.validate', [
            'definition' => $this->document(),
        ]);

        self::assertFalse($result['valid']);
        self::assertSame(['missing_capability', 'missing_capability'], array_column($result['diagnostics'], 'code'));
        self::assertSame('error', $result['diagnostics'][0]['severity']);
    }

    public function testItRejectsAMissingOrMalformedDefinitionParameter(): void
    {
        $application = $this->application();

        try {
            $application->invoke('flow.validate', []);
            self::fail('A missing definition was accepted.');
        } catch (RpcFailure $exception) {
            self::assertSame(-32602, $exception->rpcCode);
            self::assertSame('Invalid definition', $exception->getMessage());
        }

        try {
            $application->invoke('flow.validate', ['definition' => '{"key":"demo"}']);
            self::fail('A string definition was accepted.');
        } catch (RpcFailure $exception) {
            self::assertSame(-32602, $exception->rpcCode);
        }
    }

    public function testItRejectsCallerSuppliedCapabilityGrants(): void
    {
        $application = $this->application();

        try {
            $application->invoke('flow.validate', [
                'definition' => $this->document(),
                'capabilities' => ['approval'],
            ]);
            self::fail('Caller-supplied capability grants were accepted.');
        } catch (RpcFailure $exception) {
            self::assertSame(-32602, $exception->rpcCode);
            self::assertSame('Capabilities are configured by the host', $exception->getMessage());
        }
    }

    public function testItReturnsStructuredDiagnosticsWithPaths(): void
    {
        $result = $this->application()->invoke('flow.validate', [
            'definition' => [
                'version' => 1,
                'steps' => [['id' => 'a', 'depends_on' => ['missing']]],
            ],
        ]);

        self::assertFalse($result['valid']);
        $codes = array_column($result['diagnostics'], 'code');
        self::assertContains('missing_key', $codes);
        self::assertContains('unknown_dependency', $codes);

        $missingKey = $result['diagnostics'][array_search('missing_key', $codes, true)];
        self::assertSame('/key', $missingKey['path']);
        self::assertSame('error', $missingKey['severity']);
        self::assertNull($missingKey['step_id']);
    }

    /** @param array<string, mixed> $document */
    private function document(array $document = []): array
    {
        return $document === [] ? [
            'key' => 'demo',
            'version' => 1,
            'metadata' => ['required_capabilities' => ['approval']],
            'steps' => [['id' => 'approve', 'label' => 'Approve']],
        ] : $document;
    }

    /** @param list<string>|null $grantedCapabilities */
    private function application(?FlowStepExecutor $provider = null, ?array $grantedCapabilities = null): FlowRpcApplication
    {
        $definitions = new class implements FlowDefinitionProvider {
            public function definition(string $flowKey): FlowDefinition
            {
                throw new InvalidArgumentException('flow.validate must not load registered definitions');
            }
        };

        $stores = new class implements FlowStateStoreFactory {
            public function for(FlowStateSubject $subject): ForgettableFlowStateStore
            {
                throw new RuntimeException('flow.validate must not touch a state store');
            }
        };

        return new FlowRpcApplication(
            $definitions,
            $stores,
            new FlowExecutionDispatcher([]),
            operationProviders: $provider === null ? [] : [$provider],
            grantedCapabilities: $grantedCapabilities,
        );
    }

    private function provider(): FlowStepExecutor
    {
        return new class implements FlowStepExecutor, FlowOperationProvider {
            public function supports(FlowStepDefinition $step): bool
            {
                return $step->id === 'approve';
            }

            public function execute(FlowStepInvocation $invocation): StepResult
            {
                return StepResult::completed();
            }

            public function operation(FlowStepDefinition $step): ?FlowOperation
            {
                return new FlowOperation(key: 'demo.approve', label: 'Approve', capabilities: ['approval']);
            }
        };
    }
}
