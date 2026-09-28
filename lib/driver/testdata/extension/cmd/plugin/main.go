package main

import (
	"fmt"
	"os"

	"example.com/gateway-workload"
	"github.com/webong/gateway/driver/workloadplugin"
)

func main() {
	if err := workloadplugin.Serve(workload.Workload{}, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
