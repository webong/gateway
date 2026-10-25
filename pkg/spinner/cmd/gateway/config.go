package main

import (
	"bufio"
	"os"
	"strings"
)

// config resolves the automation composition from an optional environment file
// plus the process environment. Environment variables always win over the file.
type config struct {
	// NativePluginDir holds .so guests for dynamic loading.
	NativePluginDir string
	// ExternalPluginDir holds externally installed plugin packages.
	ExternalPluginDir string
}

// loadConfig reads GATEWAY_ENV_FILE (a simple KEY=value dotenv) when set, then
// overlays the process environment.
func loadConfig() config {
	values := map[string]string{}

	if path := strings.TrimSpace(os.Getenv("GATEWAY_ENV_FILE")); path != "" {
		if file, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				key, value, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				key = strings.TrimSpace(key)
				value = strings.Trim(strings.TrimSpace(value), `"'`)
				values[key] = value
			}
			_ = file.Close()
		}
	}

	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return values[key]
	}

	return config{
		NativePluginDir:   strings.TrimSpace(get("GATEWAY_PLUGIN_DIR_NATIVE")),
		ExternalPluginDir: strings.TrimSpace(get("GATEWAY_PLUGIN_DIR")),
	}
}
