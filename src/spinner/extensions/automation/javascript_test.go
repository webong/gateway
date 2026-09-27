//go:build gateway_javascript

package automationextensions

import "testing"

func TestJavaScriptBuildTagInstallsEngine(t *testing.T) {
	if !Runtime().Supports("javascript") {
		t.Fatal("gateway_javascript did not install the JavaScript engine")
	}
}
