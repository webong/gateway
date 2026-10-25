package main

import (
	"fmt"
	"os"

	consumerintegration "example.com/gateway-arranger-consumer/integration"
	arrangerintegration "github.com/webong/gateway/arranger/integration"
)

func main() {
	if err := arrangerintegration.ServeProvisioner(consumerintegration.Provisioner{}, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
