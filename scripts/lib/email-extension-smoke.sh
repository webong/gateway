#!/usr/bin/env bash

gateway_require_email_extension_artifact() {
    GATEWAY_EMAIL_EXTENSION_URL=${GATEWAY_EMAIL_EXTENSION_URL:-}
    GATEWAY_EMAIL_EXTENSION_SHA256=${GATEWAY_EMAIL_EXTENSION_SHA256:-}
    GATEWAY_EMAIL_EXTENSION_VERSION=${GATEWAY_EMAIL_EXTENSION_VERSION:-}

    if [[ ! "$GATEWAY_EMAIL_EXTENSION_URL" =~ ^https://[^[:space:]]+$ ||
          ! "$GATEWAY_EMAIL_EXTENSION_SHA256" =~ ^[a-f0-9]{64}$ ||
          -z "$GATEWAY_EMAIL_EXTENSION_VERSION" ]]; then
        echo 'Email Kubernetes smoke requires GATEWAY_EMAIL_EXTENSION_URL (HTTPS), GATEWAY_EMAIL_EXTENSION_SHA256, and GATEWAY_EMAIL_EXTENSION_VERSION.' >&2
        return 2
    fi
}

gateway_activate_email_extension() {
    local namespace=$1
    local hostname=${2:-smtp.managed.test}

    kubectl -n "$namespace" exec deployment/planner -- php -r '
        $body = json_encode([
            "extension" => "webong/email",
            "enabled" => true,
            "version" => $argv[1],
            "artifact" => ["url" => $argv[2], "sha256" => $argv[3]],
            "configuration" => ["smtp" => [
                "address" => ":2525",
                "hostname" => $argv[4],
                "tls_cert_file" => "/etc/gateway-smtp/tls.crt",
                "tls_key_file" => "/etc/gateway-smtp/tls.key",
                "recipient_preflight" => true,
            ]],
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
            fwrite(STDERR, "Email Extension activation failed: HTTP ".($matches[1] ?? "0")." ".($response ?: "")."\n");
            exit(1);
        }
    ' "$GATEWAY_EMAIL_EXTENSION_VERSION" "$GATEWAY_EMAIL_EXTENSION_URL" "$GATEWAY_EMAIL_EXTENSION_SHA256" "$hostname"
}
