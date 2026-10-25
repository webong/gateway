<?php

declare(strict_types=1);

namespace Webong\Gateway\Dns\Http;

use Illuminate\Contracts\Validation\Factory as ValidationFactory;
use Illuminate\Database\QueryException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use InvalidArgumentException;
use LogicException;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Dns\DnsProvisionerRegistry;
use Webong\Gateway\Dns\DnsZoneProvisioningFailed;
use Webong\Gateway\Dns\DnsZoneResources;
use Webong\Gateway\Dns\DnsZoneStore;
use Webong\Gateway\RegistryRequestAuthenticator;
use Webong\Gateway\Resources\ResourcePrecondition;
use Webong\Gateway\Resources\StaleResource;

final readonly class DnsZoneController
{
    public function __construct(
        private ValidationFactory $validator,
        private RegistryRequestAuthenticator $authenticator,
        private DnsZoneStore $zones,
        private DnsProvisionerRegistry $provisioners,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        return response()->json(['data' => array_map(DnsZoneResources::zone(...), $this->zones->all())]);
    }

    public function store(Request $request): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }

        $input = $request->json()->all();
        if (array_diff(array_keys($input), ['name', 'nameservers', 'soa_email', 'ttl', 'provisioner', 'provisioner_options']) !== []) {
            return $this->invalid(['zone' => ['The request contains unsupported properties.']]);
        }
        $validator = $this->validator->make($input, [
            'name' => ['required', 'string', 'max:253', 'regex:/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$/i'],
            'nameservers' => ['sometimes', 'array', 'min:1', 'max:8'],
            'nameservers.*' => ['required', 'string', 'distinct:ignore_case', 'max:253', 'regex:/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$/i'],
            'soa_email' => ['nullable', 'string', 'max:253', 'regex:/^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.?$/i'],
            'ttl' => ['sometimes', 'integer', 'min:60', 'max:86400'],
            'provisioner' => ['sometimes', 'string', 'max:64'],
            'provisioner_options' => ['sometimes', 'array:parent_zone_id,parent_zone'],
            'provisioner_options.parent_zone_id' => ['sometimes', 'string', 'max:255'],
            'provisioner_options.parent_zone' => ['sometimes', 'string', 'max:253'],
        ]);
        if ($validator->fails()) {
            return $this->invalid($validator->errors()->toArray());
        }
        $attributes = $validator->validated();
        $name = strtolower(rtrim($attributes['name'], '.'));
        if ($this->zones->find($name) !== null) {
            return response()->json(['message' => 'DNS zone already exists.'], Response::HTTP_CONFLICT);
        }
        $provisioner = strtolower((string) ($attributes['provisioner'] ?? 'manual'));
        if (! in_array($provisioner, $this->provisioners->names(), true)) {
            return $this->invalid(['provisioner' => ['The selected DNS provisioner is not registered.']]);
        }
        $nameservers = $attributes['nameservers'] ?? [];
        if (! is_array($nameservers) || $nameservers === []) {
            return $this->invalid(['nameservers' => ['Provide the nameservers for this zone.']]);
        }

        try {
            $zone = $this->zones->create(
                name: $name,
                nameservers: array_map(static fn (string $server): string => strtolower(rtrim(trim($server), '.')), $nameservers),
                provisioner: $provisioner,
                provisionerOptions: $attributes['provisioner_options'] ?? [],
                soaEmail: $attributes['soa_email'] ?? null,
                ttl: (int) ($attributes['ttl'] ?? 60),
            );
        } catch (DnsZoneProvisioningFailed $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'data' => DnsZoneResources::zone($exception->zone),
            ], Response::HTTP_BAD_GATEWAY)->header('ETag', ResourcePrecondition::etag($exception->zone->revision));
        } catch (QueryException) {
            return response()->json(['message' => 'DNS zone already exists.'], Response::HTTP_CONFLICT);
        } catch (InvalidArgumentException|RuntimeException $exception) {
            return $this->invalid(['zone' => [$exception->getMessage()]]);
        }

        return response()->json(DnsZoneResources::zone($zone), Response::HTTP_CREATED)
            ->header('ETag', ResourcePrecondition::etag($zone->revision));
    }

    public function show(Request $request, string $zone): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);

        return $model === null
            ? response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND)
            : response()->json(DnsZoneResources::zone($model))
                ->header('ETag', ResourcePrecondition::etag($model->revision));
    }

    public function replaceRecords(Request $request, string $zone): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);
        if ($model === null) {
            return response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND);
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        $input = $request->json()->all();
        if (array_keys($input) !== ['records'] || ! is_array($input['records'])) {
            return $this->invalid(['records' => ['Provide the complete DNS record list.']]);
        }
        try {
            $updated = $this->zones->replaceRecords($model, $input['records'], $revision);
        } catch (InvalidArgumentException $exception) {
            return $this->invalid(['records' => [$exception->getMessage()]]);
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS zone has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return response()->json(DnsZoneResources::zone($updated))
            ->header('ETag', ResourcePrecondition::etag($updated->revision));
    }

    public function provision(Request $request, string $zone): JsonResponse
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);
        if ($model === null) {
            return response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND);
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            $updated = $this->zones->provision($model, $revision);

            return response()->json(DnsZoneResources::zone($updated))
                ->header('ETag', ResourcePrecondition::etag($updated->revision));
        } catch (DnsZoneProvisioningFailed $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'data' => DnsZoneResources::zone($exception->zone),
            ], Response::HTTP_BAD_GATEWAY)->header('ETag', ResourcePrecondition::etag($exception->zone->revision));
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS zone has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }
    }

    public function destroy(Request $request, string $zone): Response
    {
        if (($authorization = $this->authenticator->authorize($request)) !== null) {
            return $authorization;
        }
        $model = $this->zones->find($zone);
        if ($model === null) {
            return response()->json(['message' => 'DNS zone not found.'], Response::HTTP_NOT_FOUND);
        }
        $revision = ResourcePrecondition::revision($request);
        if ($revision === null) {
            return response()->json(['message' => 'A current If-Match revision is required.'], Response::HTTP_PRECONDITION_REQUIRED);
        }
        try {
            $this->zones->delete($model, $revision);
        } catch (LogicException $exception) {
            return response()->json(['message' => $exception->getMessage()], Response::HTTP_CONFLICT);
        } catch (DnsZoneProvisioningFailed $exception) {
            return response()->json([
                'message' => $exception->getMessage(),
                'data' => DnsZoneResources::zone($exception->zone),
            ], Response::HTTP_BAD_GATEWAY)->header('ETag', ResourcePrecondition::etag($exception->zone->revision));
        } catch (StaleResource) {
            return response()->json(['message' => 'DNS zone has changed.'], Response::HTTP_PRECONDITION_FAILED);
        }

        return response()->noContent();
    }

    /** @param array<string, mixed> $errors */
    private function invalid(array $errors): JsonResponse
    {
        return response()->json(['message' => 'The given data was invalid.', 'errors' => $errors], Response::HTTP_UNPROCESSABLE_ENTITY);
    }
}
