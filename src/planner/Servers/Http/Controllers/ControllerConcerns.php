<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Validation\ValidationException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Servers\Contracts\ServerType;
use Webong\Gateway\Servers\Models\Server;
use Webong\Gateway\Servers\Resources\ServerResources;
use Webong\Gateway\Servers\ServerTypeRegistry;

trait ControllerConcerns
{
    private function invalid(string $message, array $errors): JsonResponse
    {
        return response()->json(['message' => $message, 'errors' => $errors], Response::HTTP_UNPROCESSABLE_ENTITY);
    }

    private function validationFailure(ServerType $type, ValidationException $exception, string $resource = 'server'): JsonResponse
    {
        $label = ucfirst($type->type());

        return $this->invalid("The {$label} {$resource} is invalid.", $exception->errors());
    }

    private function serverType(Server $server, ServerTypeRegistry $types): ?ServerType
    {
        return $types->find((string) $server->type);
    }

    private function serverResource(Server $server, ServerType $type): array
    {
        return ServerResources::server($server, $type);
    }
}
