package main

import "testing"

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
