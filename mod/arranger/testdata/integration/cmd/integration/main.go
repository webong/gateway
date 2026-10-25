package main

import (
	"fmt"
	"os"

	example "example.com/gateway-integration"
	"github.com/webong/gateway/arranger/integration"
)

func main() {
	if err := integration.Serve(example.Extension{}, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
