package main

import (
	"context"

	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
)

// loadDynamicEngines loads already installed packages before the Router starts.
// Live Planner-owned desired state is reconciled after Planner is available.
func loadDynamicEngines(ctx context.Context) error {
	return automationextensions.LoadAll(ctx)
}
