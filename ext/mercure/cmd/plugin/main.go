package main

import (
	"fmt"
	"os"

	"github.com/webong/gateway/driver/workloadplugin"
	"github.com/webong/gateway/ext/mercure/runtime"
)

func main() {
	if err := workloadplugin.Serve(mercure.NewWorkload(mercure.LoadConfig()), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
