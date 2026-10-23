package main

import (
	"fmt"
	"os"

	"github.com/webong/gateway/driver/workloadplugin"
	"github.com/webong/gateway/pkg/centrifugo/runtime"
)

func main() {
	if err := workloadplugin.Serve(centrifugo.NewWorkload(centrifugo.LoadConfig()), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
