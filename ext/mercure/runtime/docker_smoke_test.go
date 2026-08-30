package mercure

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/webong/gateway/internal/provisioning"
)

func TestDockerSmoke(t *testing.T) {
	image := os.Getenv("GATEWAY_MERCURE_DOCKER_SMOKE_IMAGE")
	if image == "" {
		t.Skip("set GATEWAY_MERCURE_DOCKER_SMOKE_IMAGE to run the live Docker smoke")
	}
	driver, err := provisioning.NewDockerDriver(provisioning.DockerDriverConfig{
		Binary: "docker", NodeID: "mercure-docker-smoke", PublishHost: "127.0.0.1",
		StartTimeout: 20 * time.Second, StopTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validMercureSpec()
	spec.Driver = provisioning.DriverDocker
	process, err := driver.Start(t.Context(), spec, "11111111-1111-4111-8111-111111111111", NewWorkload(Config{
		DockerImage: image, DockerBinary: "caddy", DockerConfigPath: "/etc/caddy/Caddyfile", DockerContainerPort: 80,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = process.Stop(ctx)
	})

	endpoint := "http://" + process.Host() + ":" + strconv.Itoa(process.Port()) + "/healthz"
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
			t.Fatalf("Mercure did not become HTTP-ready at %s", endpoint)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
