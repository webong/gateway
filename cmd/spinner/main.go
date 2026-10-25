package main

import (
	"context"
	"fmt"
	"os"

	extensionautomation "github.com/webong/gateway/arranger/automation/extension"
	"github.com/webong/gateway/pkg/lua/runtime"
)

func main() {
	if err := extensionautomation.Serve(context.Background(), runtime.Plugin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
