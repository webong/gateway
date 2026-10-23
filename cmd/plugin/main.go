package main

import (
	"fmt"
	"os"

	"github.com/webong/gateway/driver/workloadplugin"
	"github.com/webong/gateway/pkg/reverb/runtime"
)

func main() {
	if err := workloadplugin.Serve(reverb.NewWorkload(reverb.LoadConfig()), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
