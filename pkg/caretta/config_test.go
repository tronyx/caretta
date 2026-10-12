package caretta

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var configEnv = []string{"PROMETHEUS_PORT", "PROMETHEUS_ENDPOINT", "POLL_INTERVAL", "RESOLVE_DNS", "TRAVERSE_UP_HIERARCHY", "LINK_TTL", "MAX_CONNECTIONS"}

func clearConfigEnv(t *testing.T) {
	for _, name := range configEnv {
		t.Setenv(name, "")
	}
}

func TestConfigDefaults(t *testing.T) {
	clearConfigEnv(t)
	assert.Equal(t, carettaConfig{
		shouldResolveDns:       false,
		prometheusPort:         ":7117",
		prometheusEndpoint:     "/metrics",
		pollingIntervalSeconds: 5,
		traverseUpHierarchy:    true,
		linkTTL:                time.Hour,
		maxConnections:         1_000_000,
	}, readConfig())
}

func TestConfigFromEnvironment(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PROMETHEUS_PORT", "9000")
	t.Setenv("PROMETHEUS_ENDPOINT", "/custom")
	t.Setenv("POLL_INTERVAL", "15")
	t.Setenv("RESOLVE_DNS", "true")
	t.Setenv("TRAVERSE_UP_HIERARCHY", "false")
	t.Setenv("LINK_TTL", "30m")
	t.Setenv("MAX_CONNECTIONS", "250000")

	assert.Equal(t, carettaConfig{
		shouldResolveDns:       true,
		prometheusPort:         ":9000",
		prometheusEndpoint:     "/custom",
		pollingIntervalSeconds: 15,
		traverseUpHierarchy:    false,
		linkTTL:                30 * time.Minute,
		maxConnections:         250_000,
	}, readConfig())
}

func TestZeroLinkTTLIsAllowed(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LINK_TTL", "0")
	assert.Equal(t, time.Duration(0), readConfig().linkTTL, "0 keeps links forever")
}

func TestInvalidConfigFallsBackToDefaults(t *testing.T) {
	invalid := map[string][]string{
		"PROMETHEUS_PORT":       {"abc", "0", "-1", "65536"},
		"POLL_INTERVAL":         {"abc", "0", "-5"},
		"RESOLVE_DNS":           {"maybe"},
		"TRAVERSE_UP_HIERARCHY": {"maybe"},
		"LINK_TTL":              {"abc", "-1h", "60"},
		"MAX_CONNECTIONS":       {"abc", "0", "-1", "4294967296"},
	}
	clearConfigEnv(t)
	defaults := readConfig()
	for name, values := range invalid {
		for _, value := range values {
			t.Run(name+"="+value, func(t *testing.T) {
				clearConfigEnv(t)
				t.Setenv(name, value)
				assert.Equal(t, defaults, readConfig())
			})
		}
	}
}
