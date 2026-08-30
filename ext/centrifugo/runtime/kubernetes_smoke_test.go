package centrifugo

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/webong/gateway/internal/provisioning"
)

func TestKubernetesSmoke(t *testing.T) {
	namespace := os.Getenv("GATEWAY_CENTRIFUGO_KUBERNETES_SMOKE_NAMESPACE")
	image := os.Getenv("GATEWAY_CENTRIFUGO_KUBERNETES_SMOKE_IMAGE")
	if namespace == "" || image == "" {
		t.Skip("set the Centrifugo Kubernetes smoke namespace and image to run this test")
	}
	driver, err := provisioning.NewKubernetesDriver(provisioning.KubernetesDriverConfig{
		KubectlBinary: "kubectl", NodeID: "centrifugo-kubernetes-smoke", Namespace: namespace,
		ImagePullPolicy: "Never", RouteMode: "port-forward", StartTimeout: 30 * time.Second, StopTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validCentrifugoSpec()
	spec.Driver = provisioning.DriverKubernetes
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
	process, err := driver.Start(t.Context(), spec, "44444444-4444-4444-8444-444444444444", NewWorkload(Config{
		KubernetesImage: image, KubernetesBinary: "centrifugo", KubernetesContainerPort: 8000,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = process.Stop(ctx)
	})
	assertHTTPReady(t, "http://"+process.Host()+":"+strconv.Itoa(process.Port())+"/health")
}

func assertHTTPReady(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become HTTP-ready at %s", endpoint)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
