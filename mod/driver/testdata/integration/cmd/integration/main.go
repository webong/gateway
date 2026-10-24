package main

import (
	"fmt"
	"os"

	"example.com/gateway-integration"
	"github.com/webong/gateway/driver/integration"
)

func main() {
	if err := integration.Serve(workload.Workload{}, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
