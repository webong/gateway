//go:build !gateway_javascript && !gateway_lua

package automationextensions

import "testing"

func TestDefaultCompositionHasNoBundledLanguages(t *testing.T) {
	runtime := Runtime()
	if runtime.Supports("javascript") || runtime.Supports("lua") {
		t.Fatal("default Gateway build unexpectedly includes a script runtime")
	}
}
