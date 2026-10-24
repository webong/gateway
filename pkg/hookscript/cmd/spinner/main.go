package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"github.com/webong/gateway/pkg/hookscript/runtime"
)

type packageManifest struct {
	Descriptor plugin.Descriptor `json:"descriptor"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		return fmt.Errorf("read hookscript package manifest: %w", err)
	}
	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode hookscript package manifest: %w", err)
	}
	guest, err := runtime.Guest(manifest.Descriptor)
	if err != nil {
		return fmt.Errorf("open hookscript automation guest: %w", err)
	}
	return jsonline.ServeStdio(context.Background(), guest)
}
