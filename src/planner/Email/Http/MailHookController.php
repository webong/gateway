<?php

declare(strict_types=1);

namespace Webong\Gateway\Email\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Email\EmailRouteRejected;
use Webong\Gateway\Email\MailHookRegistry;
use Webong\Gateway\Email\VerificationRecordMissing;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\WebProxy\Models\WebProxyEndpoint;

final readonly class MailHookController
{
    public function __construct(private RegistryRequestAuthenticator $auth, private MailHookRegistry $hooks) {}

    public function index(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }

        return response()->json(['data' => array_map($this->resource(...), $this->hooks->all())]);
    }

    public function create(Request $request): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['domain', 'local_part', 'name', 'registry']) !== []
            || ! is_string($input['domain'] ?? null) || ! preg_match('/^[a-z0-9.-]{4,253}$/D', $input['domain'])
            || (isset($input['local_part']) && (! is_string($input['local_part'])
                || preg_match('/^[a-z0-9][a-z0-9._+-]{0,63}$/D', $input['local_part']) !== 1
                || str_ends_with($input['local_part'], '.') || str_contains($input['local_part'], '..')
                || preg_match('/^srs[01]-/i', $input['local_part']) === 1))
            || (isset($input['name']) && (! is_string($input['name']) || strlen($input['name']) > 255))
            || (isset($input['registry']) && (! is_string($input['registry']) || strlen($input['registry']) > 255))) {
            return response()->json(['message' => 'Mail hook is invalid.'], 422);
        }
        try {
            $hook = $this->hooks->ensure($input['domain'], $input['name'] ?? null, $input['registry'] ?? null,
                $input['local_part'] ?? '*');
        } catch (EmailRouteRejected|VerificationRecordMissing) {
            return response()->json(['message' => 'The mail domain is not verified and active.'], 409);
        } catch (RuntimeException) {
            return response()->json(['message' => 'Mail hook setup is unavailable.'], 503);
        }

        return response()->json($this->resource($hook), 201);
    }

    public function show(Request $request, string $hook): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $model = $this->hooks->find($hook);

        return $model === null ? response()->json(['message' => 'Mail hook not found.'], 404) : response()->json($this->resource($model));
    }

    public function status(Request $request, string $hook): JsonResponse
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $input = $request->json()->all();
        if (array_keys($input) !== ['active'] || ! is_bool($input['active'])) { return response()->json(['message' => 'Only active may be changed.'], 422); }
        $model = $this->hooks->find($hook);
        if ($model === null) { return response()->json(['message' => 'Mail hook not found.'], 404); }
        try { $model = $this->hooks->setActive($model, $input['active']); }
        catch (EmailRouteRejected|VerificationRecordMissing) { return response()->json(['message' => 'The mail domain is not verified and active.'], 409); }

        return response()->json($this->resource($model));
    }

    public function delete(Request $request, string $hook): Response
    {
        if (($denied = $this->auth->authorize($request)) !== null) { return $denied; }
        $model = $this->hooks->find($hook);
        if ($model === null) { return response()->json(['message' => 'Mail hook not found.'], 404); }
        $this->hooks->delete($model);

        return response()->noContent();
    }

    private function resource(WebProxyEndpoint $hook): array
    {
        $gateway = $hook->metadata['_gateway'] ?? [];
        return ['id' => $hook->getKey(), 'domain' => $gateway['domain'] ?? null,
            'local_part' => $gateway['local_part'] ?? '*',
            'name' => $gateway['name'] ?? null, 'endpoint_key' => $hook->endpoint_key,
            'active' => (bool) $hook->is_active,
            'subscriptions_url' => '/registry/endpoints/'.$hook->endpoint_key.'/subscriptions'];
    }
}
