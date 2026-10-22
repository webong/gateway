package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	php "github.com/webong/gateway/ext/frankenphp/runtime"
	"github.com/webong/gateway/router"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := router.Run(ctx, func(appDir string) (router.Planner, error) {
		return php.Start(appDir)
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
