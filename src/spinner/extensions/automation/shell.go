//go:build gateway_shell

package automationextensions

import (
	"fmt"
	"os"
	"strings"

	shell "github.com/webong/gateway/ext/shell/runtime"
)

func init() {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_SHELL_ENABLED")), "true") {
		return
	}
	engine, err := shell.NewFromEnvironment()
	if err != nil {
		panic(fmt.Sprintf("configure Gateway shell extension: %v", err))
	}
	register(engine)
}
