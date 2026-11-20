package centrifugo

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/webong/gateway/pkg/spinner/provision"
	"github.com/webong/workload"
)

func TestDockerSmoke(t *testing.T) {
	image := os.Getenv("GATEWAY_CENTRIFUGO_DOCKER_SMOKE_IMAGE")
	if image == "" {
		t.Skip("set GATEWAY_CENTRIFUGO_DOCKER_SMOKE_IMAGE to run the live Docker smoke")
	}
	driver, err := workload.NewDockerDriver(workload.DockerDriverConfig{
		Binary: "docker", NodeID: "centrifugo-docker-smoke", PublishHost: "127.0.0.1",
		StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validCentrifugoSpec()
	spec.AssignmentID = "22222222-2222-4222-8222-222222222222"
	spec.Driver = provision.DriverDocker
	spec.Replicas = 1
	spec.Configuration = []byte(`{
		"client_token_hmac_secret_key":"client-smoke-secret",
		"client_allowed_origins":[],
		"client_insecure":false,
		"channel_without_namespace_allow_subscribe_for_client":false,
		"http_api_key":"api-smoke-secret",
		"engine_type":"memory",
		"prometheus_enabled":false,
		"log_level":"info"
	}`)
	assigned, err := provision.WorkloadAssignment(spec, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	process, err := driver.Start(t.Context(), assigned, provision.WorkloadDefinition(NewWorkload(Config{
		DockerImage: image, DockerBinary: "centrifugo", DockerContainerPort: 8000,
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = process.Stop(ctx)
	})

	if len(process.Endpoints()) != 1 {
		t.Fatalf("expected one ready endpoint: %+v", process.Endpoints())
	}
	observed := process.Endpoints()[0]

	endpoint := "http://" + observed.Host + ":" + strconv.Itoa(observed.Port) + "/health"
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, requestErr := http.Get(endpoint)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Centrifugo did not become HTTP-ready at %s", endpoint)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
