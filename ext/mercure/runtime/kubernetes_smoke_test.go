package mercure

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/webong/gateway/src/spinner/provision"
)

func TestKubernetesSmoke(t *testing.T) {
	namespace := os.Getenv("GATEWAY_MERCURE_KUBERNETES_SMOKE_NAMESPACE")
	image := os.Getenv("GATEWAY_MERCURE_KUBERNETES_SMOKE_IMAGE")
	if namespace == "" || image == "" {
		t.Skip("set the Mercure Kubernetes smoke namespace and image to run this test")
	}
	driver, err := provision.NewKubernetesDriver(provision.KubernetesDriverConfig{
		KubectlBinary: "kubectl", NodeID: "mercure-kubernetes-smoke", Namespace: namespace,
		ImagePullPolicy: "Never", RouteMode: "port-forward", StartTimeout: 30 * time.Second, StopTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validMercureSpec()
	spec.Driver = provision.DriverKubernetes
	process, err := driver.Start(t.Context(), spec, "33333333-3333-4333-8333-333333333333", NewWorkload(Config{
		KubernetesImage: image, KubernetesBinary: "caddy", KubernetesConfigPath: "/etc/caddy/Caddyfile", KubernetesContainerPort: 80,
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = process.Stop(ctx)
	})
	assertHTTPReady(t, "http://"+process.Host()+":"+strconv.Itoa(process.Port())+"/healthz")
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
