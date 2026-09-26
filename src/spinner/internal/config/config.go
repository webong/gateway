package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	centrifugoruntime "github.com/webong/gateway/ext/centrifugo/runtime"
	mercureruntime "github.com/webong/gateway/ext/mercure/runtime"
	reverbruntime "github.com/webong/gateway/ext/reverb/runtime"
	"github.com/webong/gateway/src/spinner/provision"
)

type Config struct {
	Runtime                string
	LaravelBackendURL      string
	LaravelBackendSocket   string
	Port                   string
	RoadRunnerEnabled      bool
	RoadRunnerConfigPath   string
	InternalToken          string
	MaxWorkers             int
	MaxQueueSize           int
	RequestTimeout         time.Duration
	MaxBodySize            int64
	MaxIdleConns           int
	MaxConnsPerHost        int
	IdleConnTimeout        time.Duration
	LogLevel               string
	ShutdownTimeout        time.Duration
	SMTPAddress            string
	SMTPHostname           string
	SMTPMaxMessageSize     int64
	SMTPMaxRecipients      int
	SMTPReadTimeout        time.Duration
	SMTPWriteTimeout       time.Duration
	SMTPPlannerTimeout     time.Duration
	SMTPTLSCertFile        string
	SMTPTLSKeyFile         string
	SMTPImplicitTLS        bool
	SMTPAuthEnabled        bool
	SMTPRelayAddress       string
	SMTPRelayLocalName     string
	SMTPRelayServerName    string
	SMTPRelayUsername      string
	SMTPRelayPassword      string
	SMTPRelayTLSMode       string
	SMTPRelayTimeout       time.Duration
	DeliverySpoolPath      string
	DeliveryMaxAttempts    int
	DeliveryInitialBackoff time.Duration
	DeliveryMaxBackoff     time.Duration
	DeliveryPollInterval   time.Duration
	SchedulesEnabled       bool
	SchedulePollInterval   time.Duration
	DNSAddress             string
	DNSZone                string
	DNSNameservers         []string
	DNSSOAEmail            string
	DNSTTL                 uint32
	DNSPlannerTimeout      time.Duration
	DNSReadTimeout         time.Duration
	DNSWriteTimeout        time.Duration
	DNSMaxResponseRecords  int
	DNSRateLimit           int
	DNSRateBurst           int
	Provisioning           provision.HostConfig
	Reverb                 reverbruntime.Config
	Mercure                mercureruntime.Config
	Centrifugo             centrifugoruntime.Config
}

func LoadConfig() (*Config, error) {
	runtime := getEnv("GATEWAY_RUNTIME", "")
	if runtime == "" {
		runtime = "http"
	}
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime != "roadrunner" && runtime != "http" && runtime != "diagnostic" {
		return nil, fmt.Errorf("unsupported GATEWAY_RUNTIME %q (expected roadrunner, http, or diagnostic)", runtime)
	}

	provisioningConfig, err := provision.LoadConfig(runtime)
	if err != nil {
		return nil, err
	}
	reverbConfig := reverbruntime.LoadConfig()
	mercureConfig := mercureruntime.LoadConfig()
	centrifugoConfig := centrifugoruntime.LoadConfig()
	if (reverbConfig.Enabled || mercureConfig.Enabled || centrifugoConfig.Enabled) && !provisioningConfig.Enabled {
		return nil, fmt.Errorf("enabled server workloads require GATEWAY_PROVISIONING_ENABLED")
	}

	dnsAddress := getEnv("GATEWAY_DNS_ADDR", "")
	dnsZone := getEnv("GATEWAY_DNS_ZONE", "")
	dnsNameservers := getEnvList("GATEWAY_DNS_NAMESERVERS")
	if dnsAddress != "" && (dnsZone == "" || len(dnsNameservers) == 0) {
		return nil, fmt.Errorf("GATEWAY_DNS_ZONE and GATEWAY_DNS_NAMESERVERS are required when GATEWAY_DNS_ADDR is set")
	}
	dnsTTL := getEnvInt("GATEWAY_DNS_TTL", 0)
	if dnsTTL < 0 || uint64(dnsTTL) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("GATEWAY_DNS_TTL must be between 0 and %d", uint64(^uint32(0)))
	}

	config := &Config{
		Runtime:                runtime,
		LaravelBackendURL:      getEnv("GATEWAY_LARAVEL_BACKEND_URL", ""),
		LaravelBackendSocket:   getEnv("GATEWAY_LARAVEL_BACKEND_SOCKET", ""),
		Port:                   getEnv("PORT", "5001"),
		RoadRunnerEnabled:      runtime == "roadrunner",
		RoadRunnerConfigPath:   getEnv("ROADRUNNER_CONFIG", ".rr.yaml"),
		InternalToken:          getEnv("GATEWAY_INTERNAL_TOKEN", ""),
		MaxWorkers:             getEnvInt("MAX_WORKERS", 100),
		MaxQueueSize:           getEnvInt("MAX_QUEUE_SIZE", 1000),
		RequestTimeout:         getEnvDuration("REQUEST_TIMEOUT", 30*time.Second),
		MaxBodySize:            getEnvInt64("MAX_BODY_SIZE", 10*1024*1024),
		MaxIdleConns:           getEnvInt("MAX_IDLE_CONNS", 100),
		MaxConnsPerHost:        getEnvInt("MAX_CONNS_PER_HOST", 100),
		IdleConnTimeout:        getEnvDuration("IDLE_CONN_TIMEOUT", 90*time.Second),
		LogLevel:               getEnv("LOG_LEVEL", "debug"),
		ShutdownTimeout:        getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
		SMTPAddress:            getEnv("GATEWAY_SMTP_ADDR", ""),
		SMTPHostname:           getEnv("GATEWAY_SMTP_HOSTNAME", "gateway.local"),
		SMTPMaxMessageSize:     getEnvInt64("GATEWAY_SMTP_MAX_MESSAGE_SIZE", 10*1024*1024),
		SMTPMaxRecipients:      getEnvInt("GATEWAY_SMTP_MAX_RECIPIENTS", 100),
		SMTPReadTimeout:        getEnvDuration("GATEWAY_SMTP_READ_TIMEOUT", 5*time.Minute),
		SMTPWriteTimeout:       getEnvDuration("GATEWAY_SMTP_WRITE_TIMEOUT", 30*time.Second),
		SMTPPlannerTimeout:     getEnvDuration("GATEWAY_SMTP_PLANNER_TIMEOUT", 30*time.Second),
		SMTPTLSCertFile:        getEnv("GATEWAY_SMTP_TLS_CERT_FILE", ""),
		SMTPTLSKeyFile:         getEnv("GATEWAY_SMTP_TLS_KEY_FILE", ""),
		SMTPImplicitTLS:        getEnvBool("GATEWAY_SMTP_IMPLICIT_TLS", false),
		SMTPAuthEnabled:        getEnvBool("GATEWAY_SMTP_AUTH_ENABLED", false),
		SMTPRelayAddress:       getEnv("GATEWAY_SMTP_RELAY_ADDR", ""),
		SMTPRelayLocalName:     getEnv("GATEWAY_SMTP_RELAY_LOCAL_NAME", "gateway.local"),
		SMTPRelayServerName:    getEnv("GATEWAY_SMTP_RELAY_SERVER_NAME", ""),
		SMTPRelayUsername:      getEnv("GATEWAY_SMTP_RELAY_USERNAME", ""),
		SMTPRelayPassword:      getEnv("GATEWAY_SMTP_RELAY_PASSWORD", ""),
		SMTPRelayTLSMode:       strings.ToLower(getEnv("GATEWAY_SMTP_RELAY_TLS_MODE", "starttls")),
		SMTPRelayTimeout:       getEnvDuration("GATEWAY_SMTP_RELAY_TIMEOUT", 30*time.Second),
		DeliverySpoolPath:      getEnv("GATEWAY_DELIVERY_SPOOL_PATH", ""),
		DeliveryMaxAttempts:    getEnvInt("GATEWAY_DELIVERY_MAX_ATTEMPTS", 8),
		DeliveryInitialBackoff: getEnvDuration("GATEWAY_DELIVERY_INITIAL_BACKOFF", 5*time.Second),
		DeliveryMaxBackoff:     getEnvDuration("GATEWAY_DELIVERY_MAX_BACKOFF", time.Hour),
		DeliveryPollInterval:   getEnvDuration("GATEWAY_DELIVERY_POLL_INTERVAL", time.Second),
		SchedulesEnabled:       getEnvBool("GATEWAY_SCHEDULES_ENABLED", false),
		SchedulePollInterval:   getEnvDuration("GATEWAY_SCHEDULE_POLL_INTERVAL", 5*time.Second),
		DNSAddress:             dnsAddress,
		DNSZone:                dnsZone,
		DNSNameservers:         dnsNameservers,
		DNSSOAEmail:            getEnv("GATEWAY_DNS_SOA_EMAIL", ""),
		DNSTTL:                 uint32(dnsTTL),
		DNSPlannerTimeout:      getEnvDuration("GATEWAY_DNS_PLANNER_TIMEOUT", 750*time.Millisecond),
		DNSReadTimeout:         getEnvDuration("GATEWAY_DNS_READ_TIMEOUT", 2*time.Second),
		DNSWriteTimeout:        getEnvDuration("GATEWAY_DNS_WRITE_TIMEOUT", 2*time.Second),
		DNSMaxResponseRecords:  getEnvInt("GATEWAY_DNS_MAX_RESPONSE_RECORDS", 16),
		DNSRateLimit:           getEnvInt("GATEWAY_DNS_RATE_LIMIT", 5000),
		DNSRateBurst:           getEnvInt("GATEWAY_DNS_RATE_BURST", 10000),
		Provisioning:           provisioningConfig,
		Reverb:                 reverbConfig,
		Mercure:                mercureConfig,
		Centrifugo:             centrifugoConfig,
	}
	if config.LaravelBackendSocket != "" && config.Runtime != "http" {
		return nil, fmt.Errorf("GATEWAY_LARAVEL_BACKEND_SOCKET requires GATEWAY_RUNTIME=http")
	}
	if config.SchedulesEnabled && config.Runtime != "http" {
		return nil, fmt.Errorf("GATEWAY_SCHEDULES_ENABLED currently requires GATEWAY_RUNTIME=http")
	}
	if config.SchedulePollInterval <= 0 {
		return nil, fmt.Errorf("GATEWAY_SCHEDULE_POLL_INTERVAL must be positive")
	}
	if (config.SMTPRelayUsername == "") != (config.SMTPRelayPassword == "") {
		return nil, fmt.Errorf("GATEWAY_SMTP_RELAY_USERNAME and GATEWAY_SMTP_RELAY_PASSWORD must be configured together")
	}
	if config.SMTPRelayTLSMode != "none" && config.SMTPRelayTLSMode != "starttls" && config.SMTPRelayTLSMode != "implicit" {
		return nil, fmt.Errorf("unsupported GATEWAY_SMTP_RELAY_TLS_MODE %q (expected none, starttls, or implicit)", config.SMTPRelayTLSMode)
	}
	if config.SMTPRelayUsername != "" && config.SMTPRelayTLSMode == "none" {
		return nil, fmt.Errorf("GATEWAY_SMTP_RELAY_USERNAME requires outbound SMTP TLS")
	}
	if config.SMTPRelayTimeout <= 0 {
		return nil, fmt.Errorf("GATEWAY_SMTP_RELAY_TIMEOUT must be positive")
	}
	if config.SMTPRelayAddress != "" && strings.TrimSpace(config.DeliverySpoolPath) == "" {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_SPOOL_PATH is required when GATEWAY_SMTP_RELAY_ADDR is set")
	}
	if config.DeliveryMaxAttempts < 1 || config.DeliveryInitialBackoff <= 0 || config.DeliveryMaxBackoff < config.DeliveryInitialBackoff || config.DeliveryPollInterval <= 0 {
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
