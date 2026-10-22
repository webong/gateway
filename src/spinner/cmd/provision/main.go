// gateway-provision runs a target-bound worker without a public traffic listener.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/webong/gateway/src/spinner/internal/logging"
	"github.com/webong/gateway/src/spinner/provision"
)

func main() {
	check := flag.Bool("check", false, "verify target connection and permissions without changing workloads")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	config, err := provision.LoadConfig("http")
	if err != nil {
		return err
	}
	if config.TargetID == "" || config.TargetToken == "" {
		return fmt.Errorf("GATEWAY_PROVISIONING_TARGET_ID and GATEWAY_PROVISIONING_TARGET_TOKEN are required")
	}
	plannerURL := os.Getenv("GATEWAY_PLANNER_URL")
	client := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if check {
		checkContext, cancelCheck := context.WithTimeout(ctx, config.StartTimeout)
		defer cancelCheck()
		if err := provision.CheckTarget(checkContext, config.Config, plannerURL, client); err != nil {
			return err
		}
		fmt.Println("Target connection checks passed. No workloads changed.")
		return nil
	}
	paths, err := provision.PluginPaths()
	if err != nil {
		return err
	}
	plugins, workloads, err := provision.LoadPlugins(paths)
	if err != nil {
		return err
	}
	defer plugins.Close()
	if len(workloads) == 0 {
		return fmt.Errorf("configure at least one executable in GATEWAY_EXTENSION_PATHS")
	}
	runtime, err := provision.Start(config.Config, plannerURL, "", client, logging.NewLogger(os.Getenv("GATEWAY_LOG_LEVEL")), workloads...)
	if err != nil {
		return err
	}
	defer runtime.Stop()
	<-ctx.Done()
	return nil
}
