//go:build gateway_lua

package automationextensions

import lua "github.com/webong/gateway/ext/lua/runtime"

func init() {
	register(lua.New())
}
