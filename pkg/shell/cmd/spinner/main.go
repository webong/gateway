package main

import (
	"context"
	"fmt"
	"os"

	extensionautomation "github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/pkg/shell/runtime"
)

func main() {
	if err := extensionautomation.ServeWithHandler(context.Background(), runtime.Plugin, runtime.HandleHostOperation); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
