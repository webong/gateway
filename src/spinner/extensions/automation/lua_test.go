//go:build gateway_lua

package automationextensions

import "testing"

func TestLuaBuildTagInstallsEngine(t *testing.T) {
	if !Runtime().Supports("lua") {
		t.Fatal("gateway_lua did not install the Lua engine")
	}
}
