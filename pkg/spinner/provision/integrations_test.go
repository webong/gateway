package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableCentrifugoWorkloadLoadsWithoutHostImport(t *testing.T) {
	t.Setenv("GATEWAY_CENTRIFUGO_DOCKER_IMAGE", "centrifugo/centrifugo:v6")
	path := filepath.Join(t.TempDir(), "gateway-centrifugo")
	build := exec.Command("go", "build", "-mod=readonly", "-o", path, "./cmd/integration")
	build.Dir = filepath.Join("..", "..", "..", "pkg", "centrifugo")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build independently loadable integration: %v\n%s", err, output)
	}
	integrations, workloads, err := LoadIntegrations([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	defer integrations.Close()
	if len(workloads) != 1 || workloads[0].Type() != "centrifugo" || !workloads[0].Supports(DriverDocker) {
		t.Fatalf("unexpected integration capabilities: %v", workloads)
	}
	spec := ServerSpec{
		AssignmentID: "assignment-1", ID: "server-1", Type: "centrifugo", Name: "events",
		Hostname: "events.example.test", Driver: DriverDocker, DesiredState: DesiredRunning,
		Replicas: 1, Revision: 1,
		Configuration: []byte(`{"client_token_hmac_secret_key":"secret","http_api_key":"api-secret","engine_type":"memory"}`),
	}
	if err := workloads[0].Validate(spec); err != nil {
		t.Fatal(err)
	}
	launch, err := workloads[0].Launch(spec, DriverDocker, "0.0.0.0", 0)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Image != "centrifugo/centrifugo:v6" || launch.Port != 8000 || !strings.Contains(strings.Join(launch.Environment, "\n"), "CENTRIFUGO_HTTP_API_KEY=api-secret") {
		t.Fatalf("unexpected Centrifugo launch: %+v", launch)
	}
	spec.Configuration = []byte(`{"client_token_hmac_secret_key":"secret","http_api_key":"api-secret","engine_type":"invalid"}`)
	if err := workloads[0].Validate(spec); err == nil || !strings.Contains(err.Error(), "unsupported engine") {
		t.Fatalf("expected integration validation failure, got %v", err)
	}
}

func TestIntegrationLoaderRejectsMissingExecutableAndRelativePaths(t *testing.T) {
	if _, err := OpenIntegration("relative/integration"); err == nil {
		t.Fatal("accepted relative path")
	}
	if _, err := OpenIntegration(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("accepted missing executable")
	}
	t.Setenv("GATEWAY_INTEGRATION_PATHS", "relative/integration")
	if _, err := IntegrationPaths(); err == nil {
		t.Fatal("accepted relative configured path")
	}
	t.Setenv("GATEWAY_INTEGRATION_PATHS", "/opt/gateway/integrations/centrifugo"+string(os.PathListSeparator)+"/opt/gateway/integrations/mercure")
	paths, err := IntegrationPaths()
	if err != nil || len(paths) != 2 {
		t.Fatalf("unexpected paths %v: %v", paths, err)
	}
}
