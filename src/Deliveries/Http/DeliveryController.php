<?php

declare(strict_types=1);

namespace Webong\Gateway\Deliveries\Http;

use DateTimeImmutable;
use Exception;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Http\Client\Response as ClientResponse;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use RuntimeException;
use Symfony\Component\HttpFoundation\Response;
use Webong\Gateway\Deliveries\DeliveryAdminClient;
use Webong\Gateway\RegistryRequestAuthenticator;

final readonly class DeliveryController
{
    public function __construct(
        private RegistryRequestAuthenticator $authenticator,
        private DeliveryAdminClient $admin,
    ) {}

    public function index(Request $request): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }

        $eventId = $request->query('event_id');
        $filterByEvent = array_key_exists('event_id', $request->query());
        $state = $filterByEvent ? null : $request->query('state', 'failed');
        $limit = $request->query('limit', '50');
        if (($filterByEvent && (! $this->validEventId($eventId) || array_key_exists('state', $request->query())))
            || (! $filterByEvent && (! is_string($state) || ! in_array($state, ['queued', 'deferred', 'failed', 'delivered'], true)))
            || ! is_scalar($limit) || filter_var((string) $limit, FILTER_VALIDATE_INT) === false
            || (int) $limit < 1 || (int) $limit > 100) {
            return response()->json(['message' => 'Invalid delivery filter or limit.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        $items = [];
        try {
            foreach ($this->admin->nodes() as $node => $_) {
                $query = $filterByEvent
                    ? ['event_id' => $eventId, 'limit' => (int) $limit]
                    : ['state' => $state, 'limit' => (int) $limit];
                $response = $this->admin->get('/deliveries', $query, $node);
                if (! $response->ok() || ! is_array($response->json('data'))) {
                    return $this->unavailable();
                }
                foreach ($response->json('data') as $item) {
                    $resource = $this->resource($item);
                    if ($resource === null || ($filterByEvent && ($resource['event_id'] ?? null) !== $eventId)
                        || (! $filterByEvent && $resource['state'] !== $state)) {
                        return $this->unavailable();
                    }
                    try {
                        $resource['_order'] = isset($resource['updated_at'])
                            ? (new DateTimeImmutable($resource['updated_at']))->format('Uu') : '0';
                    } catch (Exception) {
                        return $this->unavailable();
                    }
                    $resource['node'] = $node;
                    $items[] = $resource;
                }
            }
        } catch (ConnectionException|RuntimeException) {
            return $this->unavailable();
        }
        usort($items, static function (array $left, array $right): int {
            return strcmp($right['_order'], $left['_order'])
                ?: strcmp($left['node'].$left['id'], $right['node'].$right['id']);
        });
        if (count($items) > (int) $limit) {
            $items = array_slice($items, 0, (int) $limit);
        }
        foreach ($items as &$item) {
            unset($item['_order']);
        }
        unset($item);

        return response()->json(['data' => $items]);
    }

    public function show(Request $request, string $delivery): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        if (! $this->validId($delivery)) {
            return $this->notFound();
        }

        try {
            foreach ($this->admin->nodes() as $node => $_) {
                $response = $this->admin->get('/deliveries/'.$delivery, [], $node);
                if ($response->status() === Response::HTTP_NOT_FOUND) {
                    continue;
                }

                return $this->statusResponse($response, Response::HTTP_OK, $delivery, $node);
            }
        } catch (ConnectionException|RuntimeException) {
            return $this->unavailable();
        }

        return $this->notFound();
    }

    public function retry(Request $request, string $delivery): JsonResponse
    {
        if ($error = $this->authenticator->authorize($request)) {
            return $error;
        }
        if (! $this->validId($delivery)) {
            return $this->notFound();
        }
        if ($request->getContent() !== '') {
            return response()->json(['message' => 'Retry does not accept a request body.'], Response::HTTP_UNPROCESSABLE_ENTITY);
        }

        try {
            $nodes = $this->admin->nodes();
            if (count($nodes) === 1) {
                $node = array_key_first($nodes);
            } else {
                $node = null;
                foreach ($nodes as $candidate => $_) {
                    $status = $this->admin->get('/deliveries/'.$delivery, [], $candidate);
                    if ($status->status() === Response::HTTP_NOT_FOUND) {
                        continue;
                    }
                    if ($status->status() !== Response::HTTP_OK
                        || ($resource = $this->resource($status->json())) === null
                        || $resource['id'] !== $delivery) {
                        return $this->unavailable();
                    }
                    $node = $candidate;
                    break;
                }
                if ($node === null) {
                    return $this->notFound();
                }
            }
            $response = $this->admin->post('/deliveries/'.$delivery.'/retry', $node);
        } catch (ConnectionException|RuntimeException) {
            return $this->unavailable();
        }
        if ($response->status() === Response::HTTP_NOT_FOUND) {
            return $this->notFound();
        }
        if ($response->status() === Response::HTTP_CONFLICT) {
            return response()->json(['message' => 'Delivery is already pending.'], Response::HTTP_CONFLICT);
        }

        return $this->statusResponse($response, Response::HTTP_ACCEPTED, $delivery, $node);
    }

    private function statusResponse(ClientResponse $response, int $expectedStatus, string $delivery, string $node): JsonResponse
    {
        if ($response->status() !== $expectedStatus || ($resource = $this->resource($response->json())) === null
            || $resource['id'] !== $delivery) {
            return $this->unavailable();
        }

        $resource['node'] = $node;

        return response()->json($resource, $expectedStatus);
    }

    /** @return array<string, int|string>|null */
    private function resource(mixed $value): ?array
    {
        if (! is_array($value) || ! $this->validId($value['id'] ?? null)
            || ! in_array($value['state'] ?? null, ['queued', 'deferred', 'failed', 'delivered'], true)
            || ! is_int($value['attempts'] ?? null)
            || (array_key_exists('event_id', $value) && ! $this->validEventId($value['event_id']))
            || (array_key_exists('follow_up_id', $value) && ! $this->validId($value['follow_up_id']))) {
            return null;
        }
        $resource = [
            'id' => $value['id'],
            'state' => $value['state'],
            'attempts' => $value['attempts'],
        ];
        foreach (['event_id', 'protocol', 'adapter', 'subscriber_id', 'next_attempt_at', 'created_at', 'updated_at'] as $field) {
            if (isset($value[$field]) && is_string($value[$field])) {
                $resource[$field] = $value[$field];
            }
        }
        if (array_key_exists('follow_up_id', $value)) {
            $resource['follow_up_id'] = $value['follow_up_id'];
        }
        foreach (['expected_status', 'response_status'] as $field) {
            if (array_key_exists($field, $value)) {
                if (! is_int($value[$field]) || $value[$field] < 100 || $value[$field] > 599) {
                    return null;
                }
                $resource[$field] = $value[$field];
            }
        }
        if (array_key_exists('body_assertion', $value)) {
            if (! in_array($value['body_assertion'], ['matched', 'mismatched', 'acceptance_unknown'], true)) {
                return null;
            }
            $resource['body_assertion'] = $value['body_assertion'];
        }

        return $resource;
    }

    private function validId(mixed $value): bool
    {
        return is_string($value) && preg_match('/\A[0-9a-f]{32}\z/D', $value) === 1;
    }

    private function validEventId(mixed $value): bool
    {
        return is_string($value) && strlen($value) <= 255
            && preg_match('/\A[A-Za-z0-9._:-]+\z/D', $value) === 1;
    }

    private function unavailable(): JsonResponse
    {
        return response()->json(['message' => 'Delivery status is unavailable.'], Response::HTTP_SERVICE_UNAVAILABLE);
    }

    private function notFound(): JsonResponse
    {
        return response()->json(['message' => 'Delivery not found.'], Response::HTTP_NOT_FOUND);
    }
}
