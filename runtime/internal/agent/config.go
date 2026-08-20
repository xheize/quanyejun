package agent

import (
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxTurns     = 8
	defaultMaxToolCalls = 24
	defaultRunTimeout   = 5 * time.Minute
)

type HarnessConfig struct {
	MaxTurns     int
	MaxToolCalls int
	RunTimeout   time.Duration
}

func DefaultHarnessConfig() HarnessConfig {
	return HarnessConfig{
		MaxTurns:     defaultMaxTurns,
		MaxToolCalls: defaultMaxToolCalls,
		RunTimeout:   defaultRunTimeout,
	}
}

func ConfigFromEnvironment(getenv func(string) string) HarnessConfig {
	config := DefaultHarnessConfig()
	config.MaxTurns = boundedInt(getenv("QUANYEJUN_AGENT_MAX_TURNS"), config.MaxTurns, 1, 32)
	config.MaxToolCalls = boundedInt(getenv("QUANYEJUN_AGENT_MAX_TOOL_CALLS"), config.MaxToolCalls, 1, 128)
	timeoutSeconds := boundedInt(getenv("QUANYEJUN_AGENT_RUN_TIMEOUT_SECONDS"), int(config.RunTimeout/time.Second), 10, 3600)
	config.RunTimeout = time.Duration(timeoutSeconds) * time.Second
	return config
}

func normalizeHarnessConfig(config HarnessConfig) HarnessConfig {
	defaults := DefaultHarnessConfig()
	if config.MaxTurns < 1 || config.MaxTurns > 32 {
		config.MaxTurns = defaults.MaxTurns
	}
	if config.MaxToolCalls < 1 || config.MaxToolCalls > 128 {
		config.MaxToolCalls = defaults.MaxToolCalls
	}
	if config.RunTimeout < 10*time.Second || config.RunTimeout > time.Hour {
		config.RunTimeout = defaults.RunTimeout
	}
	return config
}

func boundedInt(raw string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}
