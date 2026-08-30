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
	"github.com/webong/gateway/internal/provisioning"
)

type Config struct {
	Runtime              string
	LaravelBackendURL    string
	Port                 string
	RoadRunnerEnabled    bool
	RoadRunnerConfigPath string
	InternalToken        string
	MaxWorkers           int
	MaxQueueSize         int
	RequestTimeout       time.Duration
	MaxBodySize          int64
	MaxIdleConns         int
	MaxConnsPerHost      int
	IdleConnTimeout      time.Duration
	LogLevel             string
	ShutdownTimeout      time.Duration
	SMTPAddress          string
	SMTPHostname         string
	SMTPMaxMessageSize   int64
	SMTPMaxRecipients    int
	SMTPReadTimeout      time.Duration
	SMTPWriteTimeout     time.Duration
	SMTPPlannerTimeout   time.Duration
	SMTPTLSCertFile      string
	SMTPTLSKeyFile       string
	SMTPImplicitTLS      bool
	SMTPAuthEnabled      bool
	Provisioning         provisioning.HostConfig
	Reverb               reverbruntime.Config
	Mercure              mercureruntime.Config
	Centrifugo           centrifugoruntime.Config
}

func LoadConfig() (*Config, error) {
	runtime := getEnv("GATEWAY_RUNTIME", "")
	if runtime == "" {
		runtime = "standalone"
	}
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime != "roadrunner" && runtime != "http" && runtime != "standalone" {
		return nil, fmt.Errorf("unsupported GATEWAY_RUNTIME %q (expected roadrunner, http, or standalone)", runtime)
	}

	provisioningConfig, err := provisioning.LoadConfig(runtime)
	if err != nil {
		return nil, err
	}
	reverbConfig := reverbruntime.LoadConfig()
	mercureConfig := mercureruntime.LoadConfig()
	centrifugoConfig := centrifugoruntime.LoadConfig()
	if (reverbConfig.Enabled || mercureConfig.Enabled || centrifugoConfig.Enabled) && !provisioningConfig.Enabled {
		return nil, fmt.Errorf("enabled server workloads require GATEWAY_PROVISIONING_ENABLED")
	}

	config := &Config{
		Runtime:              runtime,
		LaravelBackendURL:    getEnv("GATEWAY_LARAVEL_BACKEND_URL", ""),
		Port:                 getEnv("PORT", "5001"),
		RoadRunnerEnabled:    runtime == "roadrunner",
		RoadRunnerConfigPath: getEnv("ROADRUNNER_CONFIG", ".rr.yaml"),
		InternalToken:        getEnv("GATEWAY_INTERNAL_TOKEN", ""),
		MaxWorkers:           getEnvInt("MAX_WORKERS", 100),
		MaxQueueSize:         getEnvInt("MAX_QUEUE_SIZE", 1000),
		RequestTimeout:       getEnvDuration("REQUEST_TIMEOUT", 30*time.Second),
		MaxBodySize:          getEnvInt64("MAX_BODY_SIZE", 10*1024*1024),
		MaxIdleConns:         getEnvInt("MAX_IDLE_CONNS", 100),
		MaxConnsPerHost:      getEnvInt("MAX_CONNS_PER_HOST", 100),
		IdleConnTimeout:      getEnvDuration("IDLE_CONN_TIMEOUT", 90*time.Second),
		LogLevel:             getEnv("LOG_LEVEL", "debug"),
		ShutdownTimeout:      getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
		SMTPAddress:          getEnv("GATEWAY_SMTP_ADDR", ""),
		SMTPHostname:         getEnv("GATEWAY_SMTP_HOSTNAME", "gateway.local"),
		SMTPMaxMessageSize:   getEnvInt64("GATEWAY_SMTP_MAX_MESSAGE_SIZE", 10*1024*1024),
		SMTPMaxRecipients:    getEnvInt("GATEWAY_SMTP_MAX_RECIPIENTS", 100),
		SMTPReadTimeout:      getEnvDuration("GATEWAY_SMTP_READ_TIMEOUT", 5*time.Minute),
		SMTPWriteTimeout:     getEnvDuration("GATEWAY_SMTP_WRITE_TIMEOUT", 30*time.Second),
		SMTPPlannerTimeout:   getEnvDuration("GATEWAY_SMTP_PLANNER_TIMEOUT", 30*time.Second),
		SMTPTLSCertFile:      getEnv("GATEWAY_SMTP_TLS_CERT_FILE", ""),
		SMTPTLSKeyFile:       getEnv("GATEWAY_SMTP_TLS_KEY_FILE", ""),
		SMTPImplicitTLS:      getEnvBool("GATEWAY_SMTP_IMPLICIT_TLS", false),
		SMTPAuthEnabled:      getEnvBool("GATEWAY_SMTP_AUTH_ENABLED", false),
		Provisioning:         provisioningConfig,
		Reverb:               reverbConfig,
		Mercure:              mercureConfig,
		Centrifugo:           centrifugoConfig,
	}

	return config, nil
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
