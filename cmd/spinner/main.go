package main

import (
	"context"
	"fmt"
	"os"

	extensionautomation "github.com/webong/gateway/extension/automation"
	"github.com/webong/gateway/pkg/javascript/runtime"
)

func main() {
	if err := extensionautomation.Serve(context.Background(), runtime.Plugin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
