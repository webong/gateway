#!/usr/bin/env bash

gateway_require_dns_extension_artifact() {
    GATEWAY_DNS_EXTENSION_URL=${GATEWAY_DNS_EXTENSION_URL:-}
    GATEWAY_DNS_EXTENSION_SHA256=${GATEWAY_DNS_EXTENSION_SHA256:-}
    GATEWAY_DNS_EXTENSION_VERSION=${GATEWAY_DNS_EXTENSION_VERSION:-}

    if [[ ! "$GATEWAY_DNS_EXTENSION_URL" =~ ^https://[^[:space:]]+$ ||
          ! "$GATEWAY_DNS_EXTENSION_SHA256" =~ ^[a-f0-9]{64}$ ||
          -z "$GATEWAY_DNS_EXTENSION_VERSION" ]]; then
        echo 'DNS Kubernetes smoke requires GATEWAY_DNS_EXTENSION_URL (HTTPS), GATEWAY_DNS_EXTENSION_SHA256, and GATEWAY_DNS_EXTENSION_VERSION.' >&2
        return 2
    fi
}

gateway_activate_dns_extension() {
    local namespace=$1
    local public_listener=${2:-true}

    kubectl -n "$namespace" exec deployment/planner -- php -r '
        $configuration = [
            "planner_timeout_ms" => 750,
            "read_timeout_ms" => 2000,
            "write_timeout_ms" => 2000,
            "max_response_records" => 16,
            "queries_per_second" => 5000,
            "query_burst" => 10000,
        ];
        if ($argv[4] === "true") {
            $configuration["address"] = ":5353";
        }
        $body = json_encode([
            "extension" => "webong/dns",
            "enabled" => true,
            "version" => $argv[1],
            "artifact" => ["url" => $argv[2], "sha256" => $argv[3]],
            "configuration" => $configuration,
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
            fwrite(STDERR, "DNS Extension activation failed: HTTP ".($matches[1] ?? "0")." ".($response ?: "")."\n");
            exit(1);
        }
    ' "$GATEWAY_DNS_EXTENSION_VERSION" "$GATEWAY_DNS_EXTENSION_URL" "$GATEWAY_DNS_EXTENSION_SHA256" "$public_listener"
}
