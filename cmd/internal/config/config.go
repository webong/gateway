package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                    string
	RoadRunnerEnabled       bool
	RoadRunnerConfigPath    string
	RoadRunnerInternalToken string
	MaxWorkers              int
	MaxQueueSize            int
	RequestTimeout          time.Duration
	MaxBodySize             int64
	MaxIdleConns            int
	MaxConnsPerHost         int
	IdleConnTimeout         time.Duration
	LogLevel                string
	ShutdownTimeout         time.Duration
}

func LoadConfig() (*Config, error) {
	return &Config{
		Port:                    getEnv("PORT", "5001"),
		RoadRunnerEnabled:       getEnvBool("ROADRUNNER_ENABLED", false),
		RoadRunnerConfigPath:    getEnv("ROADRUNNER_CONFIG", ".rr.yaml"),
		RoadRunnerInternalToken: getEnv("ROADRUNNER_INTERNAL_TOKEN", ""),
		MaxWorkers:              getEnvInt("MAX_WORKERS", 100),
		MaxQueueSize:            getEnvInt("MAX_QUEUE_SIZE", 1000),
		RequestTimeout:          getEnvDuration("REQUEST_TIMEOUT", 30*time.Second),
		MaxBodySize:             getEnvInt64("MAX_BODY_SIZE", 10*1024*1024),
		MaxIdleConns:            getEnvInt("MAX_IDLE_CONNS", 100),
		MaxConnsPerHost:         getEnvInt("MAX_CONNS_PER_HOST", 100),
		IdleConnTimeout:         getEnvDuration("IDLE_CONN_TIMEOUT", 90*time.Second),
		LogLevel:                getEnv("LOG_LEVEL", "debug"),
		ShutdownTimeout:         getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
	}, nil
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

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
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
