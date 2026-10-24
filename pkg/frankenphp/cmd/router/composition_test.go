package main

import (
	"context"
	"testing"
)

func TestShellEngineIsOptIn(t *testing.T) {
	for value, expected := range map[string]bool{
		"": false, "false": false, "0": false, "true": true, "1": true, "YES": true, "on": true,
	} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("GATEWAY_SHELL_ENABLED", value)
			if actual := shellEnabled(); actual != expected {
				t.Fatalf("shellEnabled() = %v, want %v", actual, expected)
			}
		})
	}
}

func TestDynamicEngineLoadingUsesDeploymentDirectories(t *testing.T) {
	t.Setenv("GATEWAY_PLUGIN_DIR", t.TempDir())
	t.Setenv("GATEWAY_PLUGIN_DIR_NATIVE", "")
	if err := loadDynamicEngines(context.Background()); err != nil {
		t.Fatal(err)
	}
}
