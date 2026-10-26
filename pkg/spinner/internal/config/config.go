package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/webong/gateway/pkg/spinner/networkpolicy"
	"github.com/webong/gateway/pkg/spinner/provision"
)

type Config struct {
	Runtime                   string
	PlannerURL                string
	PlannerSocket             string
	Port                      string
	InternalToken             string
	MaxWorkers                int
	MaxQueueSize              int
	RequestTimeout            time.Duration
	AutomationExecutor        string
	AutomationWorkers         int
	AutomationQueueSize       int
	AutomationTimeout         time.Duration
	HTTPPrivateNetworks       []netip.Prefix
	MaxBodySize               int64
	MaxIdleConns              int
	MaxConnsPerHost           int
	IdleConnTimeout           time.Duration
	LogLevel                  string
	ShutdownTimeout           time.Duration
	DeliverySpoolPath         string
	DeliveryDatabaseURL       string
	DeliveryAdminAddress      string
	DeliveryAdminAdvertiseURL string
	DeliveryMaxAttempts       int
	DeliveryInitialBackoff    time.Duration
	DeliveryMaxBackoff        time.Duration
	DeliveryPollInterval      time.Duration
	DeliveryReceiptRetention  time.Duration
	SchedulesEnabled          bool
	SchedulePollInterval      time.Duration
	Provisioning              provision.HostConfig
	ProvisioningRouteProxy    bool
	ProvisioningRouteCIDRs    []string
}

func LoadConfig() (*Config, error) {
	runtime := getEnv("GATEWAY_RUNTIME", "")
	if runtime == "" {
		runtime = "http"
	}
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime != "http" && runtime != "diagnostic" {
		return nil, fmt.Errorf("unsupported GATEWAY_RUNTIME %q (expected http or diagnostic)", runtime)
	}
	return loadConfig(runtime)
}

// LoadRouterConfig loads Spinner settings for a Router that already owns its
// in-process Planner adapter. It never selects a network backend from the
// standalone Spinner runtime setting.
func LoadRouterConfig() (*Config, error) {
	if value := strings.TrimSpace(os.Getenv("GATEWAY_RUNTIME")); value != "" {
		return nil, fmt.Errorf("GATEWAY_RUNTIME=%s is for standalone Spinner; the bundled Router selects its Planner adapter in-process", value)
	}
	return loadConfig("embedded")
}

func loadConfig(runtime string) (*Config, error) {
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime != "http" && runtime != "diagnostic" && runtime != "embedded" {
		return nil, fmt.Errorf("unsupported runtime %q (expected http, diagnostic, or embedded)", runtime)
	}

	provisioningConfig, err := provision.LoadConfig(runtime)
	if err != nil {
		return nil, err
	}
	httpPrivateNetworks, err := networkpolicy.ParsePrivatePrefixes(os.Getenv("GATEWAY_HTTP_PRIVATE_NETWORK_CIDRS"))
	if err != nil {
		return nil, fmt.Errorf("GATEWAY_HTTP_PRIVATE_NETWORK_CIDRS: %w", err)
	}
	legacyEnabled := getEnvBool("GATEWAY_REVERB_ENABLED", false) || getEnvBool("GATEWAY_MERCURE_ENABLED", false) || getEnvBool("GATEWAY_CENTRIFUGO_ENABLED", false)
	if (legacyEnabled || os.Getenv("GATEWAY_INTEGRATION_PATHS") != "") && !provisioningConfig.Enabled {
		return nil, fmt.Errorf("integrations require GATEWAY_PROVISIONING_ENABLED")
	}
	if legacyEnabled && os.Getenv("GATEWAY_INTEGRATION_PATHS") == "" {
		return nil, fmt.Errorf("set GATEWAY_INTEGRATION_PATHS to installed integrations; GATEWAY_*_ENABLED alone no longer loads Go code")
	}

	config := &Config{
		Runtime:                   runtime,
		PlannerURL:                getEnv("GATEWAY_PLANNER_URL", ""),
		PlannerSocket:             getEnv("GATEWAY_PLANNER_SOCKET", ""),
		Port:                      getEnv("PORT", "5001"),
		InternalToken:             getEnv("GATEWAY_INTERNAL_TOKEN", ""),
		MaxWorkers:                getEnvInt("MAX_WORKERS", 100),
		MaxQueueSize:              getEnvInt("MAX_QUEUE_SIZE", 1000),
		RequestTimeout:            getEnvDuration("REQUEST_TIMEOUT", 30*time.Second),
		AutomationExecutor:        strings.ToLower(strings.TrimSpace(getEnv("GATEWAY_AUTOMATION_EXECUTOR", "ergo"))),
		AutomationWorkers:         getEnvInt("GATEWAY_AUTOMATION_WORKERS", 8),
		AutomationQueueSize:       getEnvInt("GATEWAY_AUTOMATION_QUEUE_SIZE", 32),
		AutomationTimeout:         getEnvDuration("GATEWAY_AUTOMATION_TIMEOUT", 10*time.Second),
		HTTPPrivateNetworks:       httpPrivateNetworks,
		MaxBodySize:               getEnvInt64("MAX_BODY_SIZE", 10*1024*1024),
		MaxIdleConns:              getEnvInt("MAX_IDLE_CONNS", 100),
		MaxConnsPerHost:           getEnvInt("MAX_CONNS_PER_HOST", 100),
		IdleConnTimeout:           getEnvDuration("IDLE_CONN_TIMEOUT", 90*time.Second),
		LogLevel:                  getEnv("LOG_LEVEL", "debug"),
		ShutdownTimeout:           getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
		DeliverySpoolPath:         getEnv("GATEWAY_DELIVERY_SPOOL_PATH", ""),
		DeliveryDatabaseURL:       getEnv("GATEWAY_DELIVERY_DATABASE_URL", ""),
		DeliveryAdminAddress:      getEnv("GATEWAY_DELIVERY_ADMIN_ADDR", ""),
		DeliveryAdminAdvertiseURL: getEnv("GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL", ""),
		DeliveryMaxAttempts:       getEnvInt("GATEWAY_DELIVERY_MAX_ATTEMPTS", 8),
		DeliveryInitialBackoff:    getEnvDuration("GATEWAY_DELIVERY_INITIAL_BACKOFF", 5*time.Second),
		DeliveryMaxBackoff:        getEnvDuration("GATEWAY_DELIVERY_MAX_BACKOFF", time.Hour),
		DeliveryPollInterval:      getEnvDuration("GATEWAY_DELIVERY_POLL_INTERVAL", time.Second),
		DeliveryReceiptRetention:  getEnvDuration("GATEWAY_DELIVERY_RECEIPT_RETENTION", 90*24*time.Hour),
		SchedulesEnabled:          getEnvBool("GATEWAY_SCHEDULES_ENABLED", runtime == "embedded"),
		SchedulePollInterval:      getEnvDuration("GATEWAY_SCHEDULE_POLL_INTERVAL", 5*time.Second),
		Provisioning:              provisioningConfig,
		ProvisioningRouteProxy:    getEnvBool("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED", false),
		ProvisioningRouteCIDRs:    getEnvList("GATEWAY_PROVISIONING_ROUTE_CIDRS"),
	}
	if config.PlannerSocket != "" && config.Runtime != "http" {
		return nil, fmt.Errorf("GATEWAY_PLANNER_SOCKET requires an external Planner HTTP connection")
	}
	if config.SchedulesEnabled && config.Runtime != "http" && config.Runtime != "embedded" {
		return nil, fmt.Errorf("GATEWAY_SCHEDULES_ENABLED requires an HTTP or bundled Router Planner connection")
	}
	if config.Runtime == "embedded" && config.PlannerURL != "" {
		return nil, fmt.Errorf("GATEWAY_PLANNER_URL is for an external Planner; the bundled Router uses its in-process Planner")
	}
	if config.ProvisioningRouteProxy && (config.Runtime != "http" && config.Runtime != "embedded" || config.Provisioning.Enabled) {
		return nil, fmt.Errorf("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED requires HTTP Spinner or bundled Router without a local provisioning worker")
	}
	if config.ProvisioningRouteProxy && (config.Runtime == "http" && config.PlannerURL == "" || config.InternalToken == "") {
		return nil, fmt.Errorf("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED requires a Planner connection and GATEWAY_INTERNAL_TOKEN")
	}
	if config.ProvisioningRouteProxy && len(config.ProvisioningRouteCIDRs) == 0 {
		return nil, fmt.Errorf("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED requires GATEWAY_PROVISIONING_ROUTE_CIDRS")
	}
	if config.SchedulePollInterval <= 0 {
		return nil, fmt.Errorf("GATEWAY_SCHEDULE_POLL_INTERVAL must be positive")
	}
	if config.AutomationExecutor != "direct" && config.AutomationExecutor != "goroutine" && config.AutomationExecutor != "ergo" {
		return nil, fmt.Errorf("GATEWAY_AUTOMATION_EXECUTOR must be direct, goroutine, or ergo")
	}
	if config.AutomationWorkers < 1 || config.AutomationQueueSize < 0 || config.AutomationTimeout <= 0 {
		return nil, fmt.Errorf("GATEWAY_AUTOMATION_WORKERS and GATEWAY_AUTOMATION_TIMEOUT must be positive and GATEWAY_AUTOMATION_QUEUE_SIZE must be non-negative")
	}
	if strings.TrimSpace(config.DeliverySpoolPath) != "" && strings.TrimSpace(config.DeliveryDatabaseURL) != "" {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_SPOOL_PATH and GATEWAY_DELIVERY_DATABASE_URL are mutually exclusive")
	}
	if config.DeliveryAdminAddress != "" && config.InternalToken == "" {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_ADMIN_ADDR requires GATEWAY_INTERNAL_TOKEN")
	}
	if config.DeliveryAdminAdvertiseURL != "" && (config.DeliveryAdminAddress == "" || config.DeliveryDatabaseURL == "") {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL requires GATEWAY_DELIVERY_ADMIN_ADDR and shared PostgreSQL")
	}
	if config.DeliveryMaxAttempts < 1 || config.DeliveryInitialBackoff <= 0 || config.DeliveryMaxBackoff < config.DeliveryInitialBackoff || config.DeliveryPollInterval <= 0 || config.DeliveryReceiptRetention < 0 {
		return nil, fmt.Errorf("Gateway delivery spool attempts and durations are invalid")
	}

	return config, nil
}

func getEnvList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}

	return parsed
}
