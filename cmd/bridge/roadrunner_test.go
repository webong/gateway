package webrelay

// RoadRunner embedding tests.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedRoadRunnerRegistersWebRelayPlugin(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".rr.yaml")
	config := `version: "3"
server:
  command: "php worker.php"
http:
  address: "127.0.0.1:19091"
logs:
  level: error
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	runner, err := NewEmbeddedRoadRunner(configPath, nil, &recordingExecutor{}, 1024, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	runner.Stop()

	plugins := strings.Join(runner.server.Plugins(), ",")
	if !strings.Contains(plugins, RoadRunnerPluginName) {
		t.Fatalf("expected %q in RoadRunner plugins, got %s", RoadRunnerPluginName, plugins)
	}
}

func TestEmbeddedRoadRunnerRequiresInternalToken(t *testing.T) {
	_, err := NewEmbeddedRoadRunner(
		filepath.Join(t.TempDir(), ".rr.yaml"),
		nil,
		&recordingExecutor{},
		1024,
		"",
	)
	if err == nil {
		t.Fatal("expected embedded RoadRunner to require an internal token")
	}
}
