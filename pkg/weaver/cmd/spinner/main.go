package main

import (
	"context"
	"fmt"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	weaver "github.com/webong/gateway/pkg/weaver/runtime"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	raw, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Weaver verified package is unavailable")
		os.Exit(1)
	}
	manifest, err := packagekit.Decode(raw)
	if err != nil || manifest.VerifyArtifacts(root) != nil {
		fmt.Fprintln(os.Stderr, "Weaver artifact verification failed")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := weaver.New(root).Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Weaver runtime stopped")
		os.Exit(1)
	}
}
