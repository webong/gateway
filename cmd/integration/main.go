package main

import (
	"fmt"
	"os"

	"github.com/webong/gateway/driver/integration"
	"github.com/webong/gateway/pkg/mercure/runtime"
)

func main() {
	if err := integration.Serve(mercure.NewWorkload(mercure.LoadConfig()), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
