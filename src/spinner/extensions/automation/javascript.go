//go:build gateway_javascript

package automationextensions

import javascript "github.com/webong/gateway/ext/javascript/runtime"

func init() {
	register(javascript.New())
}
