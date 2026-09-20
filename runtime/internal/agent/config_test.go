package agent

import (
	"testing"
	"time"
)

func TestConfigFromEnvironment(t *testing.T) {
	values := map[string]string{
		"QUANYEJUN_AGENT_MAX_CONCURRENT":      "3",
		"QUANYEJUN_AGENT_MAX_TURNS":           "12",
		"QUANYEJUN_AGENT_MAX_TOOL_CALLS":      "40",
		"QUANYEJUN_AGENT_RUN_TIMEOUT_SECONDS": "90",
	}
	config := ConfigFromEnvironment(func(key string) string { return values[key] })
	if config.MaxConcurrent != 3 || config.MaxTurns != 12 || config.MaxToolCalls != 40 || config.RunTimeout != 90*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestConfigFromEnvironmentRejectsUnsafeValues(t *testing.T) {
	values := map[string]string{
		"QUANYEJUN_AGENT_MAX_CONCURRENT":      "65",
		"QUANYEJUN_AGENT_MAX_TURNS":           "0",
		"QUANYEJUN_AGENT_MAX_TOOL_CALLS":      "129",
		"QUANYEJUN_AGENT_RUN_TIMEOUT_SECONDS": "not-a-number",
	}
	config := ConfigFromEnvironment(func(key string) string { return values[key] })
	if config != DefaultHarnessConfig() {
		t.Fatalf("expected defaults, got %#v", config)
	}
}
