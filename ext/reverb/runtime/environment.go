package reverb

import (
	"strconv"

	"github.com/webong/gateway/src/spinner/provision"
)

func reverbEnvironment(spec provision.ServerSpec, config serverConfiguration) []string {
	environment := []string{
		"GATEWAY_REVERB_SERVER_ID=" + spec.ID,
		"REVERB_SCALING_ENABLED=" + strconv.FormatBool(config.ScalingEnabled),
		"REVERB_SCALING_CHANNEL=" + config.ScalingChannel,
		"REVERB_MAX_REQUEST_SIZE=" + strconv.FormatInt(config.MaxRequestSize, 10),
		"REVERB_PULSE_INGEST_INTERVAL=" + strconv.Itoa(config.PulseIngestInterval),
		"REVERB_TELESCOPE_INGEST_INTERVAL=" + strconv.Itoa(config.TelescopeIngestInterval),
	}
	environment = appendOptionalString(environment, "REDIS_URL", config.ScalingServer.URL)
	environment = appendOptionalString(environment, "REDIS_HOST", config.ScalingServer.Host)
	environment = appendOptionalInt(environment, "REDIS_PORT", config.ScalingServer.Port)
	environment = appendOptionalString(environment, "REDIS_USERNAME", config.ScalingServer.Username)
	environment = appendOptionalString(environment, "REDIS_PASSWORD", config.ScalingServer.Password)
	environment = appendOptionalInt(environment, "REDIS_DB", config.ScalingServer.Database)
	environment = appendOptionalInt(environment, "REDIS_TIMEOUT", config.ScalingServer.Timeout)

	return environment
}

func appendOptionalString(environment []string, key string, value *string) []string {
	if value == nil {
		return environment
	}

	return append(environment, key+"="+*value)
}

func appendOptionalInt(environment []string, key string, value *int) []string {
	if value == nil {
		return environment
	}

	return append(environment, key+"="+strconv.Itoa(*value))
}
