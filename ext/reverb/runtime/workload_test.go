package reverb

import (
	"strings"
	"testing"

	"github.com/webong/gateway/src/spinner/provision"
)

func TestWorkloadBuildsReverbLaunchSpecsForGenericDrivers(t *testing.T) {
	workload := NewWorkload(Config{
		PHPBinary: "php", ArtisanPath: "artisan", WorkingDirectory: "/srv/app",
		DockerImage: "example/reverb:docker", DockerPHPBinary: "php", DockerArtisanPath: "/app/artisan", DockerContainerPort: 8080,
		KubernetesImage: "example/reverb:kubernetes", KubernetesPHPBinary: "/usr/bin/php", KubernetesArtisanPath: "/app/artisan", KubernetesContainerPort: 9090,
	})
	spec := validReverbServerSpec()

	local, err := workload.Launch(spec, provision.DriverLocal, "127.0.0.1", 12000)
	if err != nil {
		t.Fatal(err)
	}
	if local.Executable != "php" || local.WorkingDirectory != "/srv/app" || local.Port != 12000 {
		t.Fatalf("unexpected local launch: %+v", local)
	}
	assertContainsAll(t, strings.Join(local.Arguments, " "), "reverb:start", "--host=127.0.0.1", "--port=12000", "--path=/socket")
	assertContainsAll(t, strings.Join(local.Environment, " "), "GATEWAY_REVERB_SERVER_ID=server-1", "REVERB_SCALING_ENABLED=true", "REDIS_PASSWORD=secret")

	docker, err := workload.Launch(spec, provision.DriverDocker, "ignored", 0)
	if err != nil {
		t.Fatal(err)
	}
	if docker.Image != "example/reverb:docker" || docker.Port != 8080 || docker.ContainerName != "reverb" {
		t.Fatalf("unexpected Docker launch: %+v", docker)
	}

	kubernetes, err := workload.Launch(spec, provision.DriverKubernetes, "ignored", 0)
	if err != nil {
		t.Fatal(err)
	}
	if kubernetes.Image != "example/reverb:kubernetes" || kubernetes.Executable != "/usr/bin/php" || kubernetes.Port != 9090 {
		t.Fatalf("unexpected Kubernetes launch: %+v", kubernetes)
	}
}

func TestWorkloadValidatesOnlyReverbConfiguration(t *testing.T) {
	workload := NewWorkload(Config{})
	spec := validReverbServerSpec()
	spec.Type = "centrifugo"
	if err := workload.Validate(spec); err == nil || !strings.Contains(err.Error(), "cannot provision") {
		t.Fatalf("expected workload type rejection, got %v", err)
	}

	spec = validReverbServerSpec()
	spec.Configuration = []byte(`{"max_request_size":0,"pulse_ingest_interval":15,"telescope_ingest_interval":15}`)
	if err := workload.Validate(spec); err == nil || !strings.Contains(err.Error(), "request size") {
		t.Fatalf("expected request limit rejection, got %v", err)
	}
}

func validReverbServerSpec() provision.ServerSpec {
	return provision.ServerSpec{
		ID: "server-1", Type: "reverb", Name: "primary", Hostname: "socket.example.test", Path: "/socket",
		Driver: provision.DriverLocal, DesiredState: provision.DesiredRunning, Replicas: 1, Revision: 1,
		Configuration: []byte(`{
			"max_request_size":10000,
			"scaling_enabled":true,
			"scaling_channel":"gateway:reverb:server-1",
			"scaling_server":{"host":"redis.internal","password":"secret"},
			"pulse_ingest_interval":15,
			"telescope_ingest_interval":15
		}`),
	}
}

func assertContainsAll(t *testing.T, actual string, expected ...string) {
	t.Helper()
	for _, value := range expected {
		if !strings.Contains(actual, value) {
			t.Fatalf("expected %q to contain %q", actual, value)
		}
	}
}
