package centrifugo

import (
	"strings"
	"testing"

	"github.com/webong/gateway/src/spinner/provision"
)

func TestWorkloadBuildsCentrifugoLaunchSpecs(t *testing.T) {
	workload := NewWorkload(Config{
		Binary: "centrifugo", WorkingDirectory: "/srv/centrifugo",
		DockerImage: "centrifugo/centrifugo:v6", DockerBinary: "centrifugo", DockerContainerPort: 8000,
		KubernetesImage: "centrifugo/centrifugo:v6", KubernetesBinary: "centrifugo", KubernetesContainerPort: 9000,
	})
	spec := validCentrifugoSpec()
	if err := workload.Validate(spec); err != nil {
		t.Fatal(err)
	}

	local, err := workload.Launch(spec, provision.DriverLocal, "127.0.0.1", 12002)
	if err != nil {
		t.Fatal(err)
	}
	if local.Executable != "centrifugo" || local.Port != 12002 || local.WorkingDirectory != "/srv/centrifugo" {
		t.Fatalf("unexpected local launch: %+v", local)
	}
	environment := strings.Join(local.Environment, " ")
	for _, expected := range []string{
		"CENTRIFUGO_HTTP_SERVER_ADDRESS=127.0.0.1",
		"CENTRIFUGO_HTTP_SERVER_PORT=12002",
		"CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY=client-secret",
		"CENTRIFUGO_ENGINE_REDIS_ADDRESS=redis://redis.internal:6379",
	} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("expected environment to contain %q: %s", expected, environment)
		}
	}

	kubernetes, err := workload.Launch(spec, provision.DriverKubernetes, "ignored", 0)
	if err != nil {
		t.Fatal(err)
	}
	if kubernetes.Image != "centrifugo/centrifugo:v6" || kubernetes.Port != 9000 {
		t.Fatalf("unexpected Kubernetes launch: %+v", kubernetes)
	}
}

func TestWorkloadRequiresRedisForMultipleReplicas(t *testing.T) {
	workload := NewWorkload(Config{Binary: "centrifugo"})
	spec := validCentrifugoSpec()
	spec.Configuration = []byte(`{
		"client_token_hmac_secret_key":"client-secret",
		"http_api_key":"api-secret",
		"engine_type":"memory",
		"log_level":"info"
	}`)
	if err := workload.Validate(spec); err == nil || !strings.Contains(err.Error(), "require the Redis engine") {
		t.Fatalf("expected Redis validation, got %v", err)
	}
}

func validCentrifugoSpec() provision.ServerSpec {
	return provision.ServerSpec{
		ID: "centrifugo-1", Type: "centrifugo", Name: "events", Hostname: "events.example.test",
		Driver: provision.DriverLocal, DesiredState: provision.DesiredRunning, Replicas: 3, Revision: 1,
		Configuration: []byte(`{
			"client_token_hmac_secret_key":"client-secret",
			"client_allowed_origins":["https://app.example.test"],
			"client_insecure":false,
			"channel_without_namespace_allow_subscribe_for_client":true,
			"http_api_key":"api-secret",
			"engine_type":"redis",
			"engine_redis_address":"redis://redis.internal:6379",
			"prometheus_enabled":true,
			"log_level":"info"
		}`),
	}
}
