package main

import (
	"fmt"
	"os"

	"github.com/webong/gateway/driver/integration"
	"github.com/webong/gateway/pkg/centrifugo/runtime"
)

func main() {
	if err := integration.Serve(centrifugo.NewWorkload(centrifugo.LoadConfig()), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
