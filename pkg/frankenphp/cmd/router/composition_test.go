package main

import (
	"context"
	"testing"
)

func TestDynamicEngineLoadingUsesDeploymentDirectories(t *testing.T) {
	t.Setenv("GATEWAY_PLUGIN_DIR", t.TempDir())
	t.Setenv("GATEWAY_PLUGIN_DIR_NATIVE", "")
	if err := loadDynamicEngines(context.Background()); err != nil {
		t.Fatal(err)
	}
}
