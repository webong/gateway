package main

import (
	"fmt"
	"os"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/reverb/runtime"
)

func main() {
	if err := integration.Serve(reverb.NewWorkload(reverb.LoadConfig()), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
