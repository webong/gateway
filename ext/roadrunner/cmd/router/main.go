package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	php "github.com/webong/gateway/ext/roadrunner/runtime"
	"github.com/webong/gateway/lib/router"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := router.RunRoadRunner(ctx, func(path string, middleware func(http.Handler) http.Handler) (router.WorkerServer, error) {
		return php.New(path, nil, middleware)
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
