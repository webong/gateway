package mercure

import (
	"strings"
	"testing"

	"github.com/webong/gateway/internal/provisioning"
)

func TestWorkloadBuildsMercureLaunchSpecs(t *testing.T) {
	workload := NewWorkload(Config{
		Binary: "mercure", WorkingDirectory: "/srv/mercure",
		DockerImage: "dunglas/mercure:v0.19", DockerBinary: "caddy", DockerConfigPath: "/etc/caddy/Caddyfile", DockerContainerPort: 80,
		KubernetesImage: "dunglas/mercure:v0.19", KubernetesBinary: "caddy", KubernetesConfigPath: "/etc/caddy/Caddyfile", KubernetesContainerPort: 8080,
	})
	spec := validMercureSpec()
	if err := workload.Validate(spec); err != nil {
		t.Fatal(err)
	}

	local, err := workload.Launch(spec, provisioning.DriverLocal, "127.0.0.1", 12001)
	if err != nil {
		t.Fatal(err)
	}
	if local.Executable != "mercure" || local.Port != 12001 || local.WorkingDirectory != "/srv/mercure" {
		t.Fatalf("unexpected local launch: %+v", local)
	}
	environment := strings.Join(local.Environment, "\n")
	for _, expected := range []string{"SERVER_NAME=http://127.0.0.1:12001", "MERCURE_PUBLISHER_JWT_KEY=publisher-secret", "anonymous", "cors_origins https://app.example.test"} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("expected environment to contain %q: %s", expected, environment)
		}
	}

	docker, err := workload.Launch(spec, provisioning.DriverDocker, "ignored", 0)
	if err != nil {
		t.Fatal(err)
	}
	if docker.Image != "dunglas/mercure:v0.19" || docker.Executable != "caddy" || docker.Port != 80 {
		t.Fatalf("unexpected Docker launch: %+v", docker)
	}
}

func TestWorkloadRejectsUnsupportedCommunityReplication(t *testing.T) {
	workload := NewWorkload(Config{Binary: "mercure"})
	spec := validMercureSpec()
	spec.Replicas = 2
	if err := workload.Validate(spec); err == nil || !strings.Contains(err.Error(), "exactly one replica") {
		t.Fatalf("expected replica validation, got %v", err)
	}
}

func validMercureSpec() provisioning.ServerSpec {
	return provisioning.ServerSpec{
		ID: "mercure-1", Type: "mercure", Name: "updates", Hostname: "updates.example.test",
		Driver: provisioning.DriverLocal, DesiredState: provisioning.DesiredRunning, Replicas: 1, Revision: 1,
		Configuration: []byte(`{
			"publisher_jwt_key":"publisher-secret",
			"publisher_jwt_algorithm":"HS256",
			"subscriber_jwt_key":"subscriber-secret",
			"subscriber_jwt_algorithm":"HS256",
			"anonymous":true,
			"cors_origins":["https://app.example.test"],
			"publish_origins":[],
			"subscriptions":true,
			"heartbeat":"40s",
			"transport":"local"
		}`),
	}
}
