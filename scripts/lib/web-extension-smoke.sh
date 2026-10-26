#!/usr/bin/env bash

gateway_require_web_extension_artifact() {
    GATEWAY_WEB_EXTENSION_URL=${GATEWAY_WEB_EXTENSION_URL:-}
    GATEWAY_WEB_EXTENSION_SHA256=${GATEWAY_WEB_EXTENSION_SHA256:-}
    GATEWAY_WEB_EXTENSION_VERSION=${GATEWAY_WEB_EXTENSION_VERSION:-}

    if [[ ! "$GATEWAY_WEB_EXTENSION_URL" =~ ^https://[^[:space:]]+$ ||
          ! "$GATEWAY_WEB_EXTENSION_SHA256" =~ ^[a-f0-9]{64}$ ||
          -z "$GATEWAY_WEB_EXTENSION_VERSION" ]]; then
        echo 'Web Kubernetes smoke requires GATEWAY_WEB_EXTENSION_URL (HTTPS), GATEWAY_WEB_EXTENSION_SHA256, and GATEWAY_WEB_EXTENSION_VERSION.' >&2
        return 2
    fi
}

gateway_activate_web_extension() {
    local namespace=$1

    kubectl -n "$namespace" exec deployment/planner -- php -r '
        $body = json_encode([
            "extension" => "webong/web",
            "enabled" => true,
            "version" => $argv[1],
            "artifact" => ["url" => $argv[2], "sha256" => $argv[3]],
            "configuration" => [
                "websocket" => ["max_connections" => 1000, "max_duration_ms" => 86400000],
                "webstream" => [
                    "heartbeat_ms" => 15000,
                    "retry_ms" => 3000,
                    "max_connections" => 1000,
                    "max_duration_ms" => 86400000,
                    "private_network_cidrs" => [],
                ],
            ],
        ], JSON_THROW_ON_ERROR);
        $context = stream_context_create(["http" => [
            "method" => "POST",
            "header" => "Authorization: Bearer ".getenv("REGISTRY_TOKEN")."\r\nContent-Type: application/json\r\n",
            "content" => $body,
            "ignore_errors" => true,
            "timeout" => 10,
        ]]);
        $response = file_get_contents("http://127.0.0.1:8080/extensions", false, $context);
        preg_match("/^HTTP\/\S+ (\d{3})/", $http_response_header[0] ?? "", $matches);
        if ((int) ($matches[1] ?? 0) !== 201) {
            fwrite(STDERR, "Web Extension activation failed: HTTP ".($matches[1] ?? "0")." ".($response ?: "")."\n");
            exit(1);
        }
    ' "$GATEWAY_WEB_EXTENSION_VERSION" "$GATEWAY_WEB_EXTENSION_URL" "$GATEWAY_WEB_EXTENSION_SHA256"
}

gateway_update_web_private_networks() {
    local namespace=$1
    local cidr=$2

    kubectl -n "$namespace" exec deployment/planner -- php -r '
        function request(string $method, string $path, ?array $body = null, ?string $etag = null): array {
            $headers = ["Authorization: Bearer ".getenv("REGISTRY_TOKEN"), "Content-Type: application/json"];
            if ($etag !== null) { $headers[] = "If-Match: \"$etag\""; }
            $context = stream_context_create(["http" => [
                "method" => $method,
                "header" => implode("\r\n", $headers)."\r\n",
                "content" => $body === null ? "" : json_encode($body, JSON_THROW_ON_ERROR),
                "ignore_errors" => true,
                "timeout" => 10,
            ]]);
            $response = file_get_contents("http://127.0.0.1:8080".$path, false, $context);
            preg_match("/^HTTP\/\S+ (\d{3})/", $http_response_header[0] ?? "", $matches);
            return [(int) ($matches[1] ?? 0), $response ?: ""];
        }
        [$status, $response] = request("GET", "/extensions");
        $rows = json_decode($response, true, 64, JSON_THROW_ON_ERROR)["data"] ?? [];
        $current = null;
        foreach ($rows as $row) {
            if (($row["extension"] ?? null) === "webong/web") { $current = $row; break; }
        }
        if ($status !== 200 || !is_array($current)) {
            fwrite(STDERR, "Active Web Extension was not found.\n"); exit(1);
        }
        $configuration = $current["configuration"] ?? [];
        $configuration["webstream"]["private_network_cidrs"] = [$argv[1]];
        [$status, $response] = request("PUT", "/extensions/".$current["id"], [
            "extension" => "webong/web",
            "enabled" => true,
            "version" => $current["version"],
            "artifact" => $current["artifact"],
            "configuration" => $configuration,
        ], (string) $current["revision"]);
        if ($status !== 200) {
            fwrite(STDERR, "Web Extension configuration update failed: HTTP $status $response\n"); exit(1);
        }
    ' "$cidr"
}
