package caretta

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultPrometheusEndpoint     = "/metrics"
	defaultPrometheusPort         = ":7117"
	defaultPollingIntervalSeconds = 5
	defaultShouldResolveDns       = false
	defaultTraverseUpHierarchy    = true
	defaultLinkTTL                = time.Hour
	defaultMaxConnections         = 1_000_000
)

type carettaConfig struct {
	shouldResolveDns       bool
	prometheusPort         string
	prometheusEndpoint     string
	pollingIntervalSeconds int
	traverseUpHierarchy    bool
	linkTTL                time.Duration
	maxConnections         uint32
}

// environment variables based, encapsulated to enable future changes
func readConfig() carettaConfig {
	port := defaultPrometheusPort
	if p := envOr("PROMETHEUS_PORT", 0, strconv.Atoi, func(v int) bool { return v > 0 && v <= 65535 }); p != 0 {
		port = fmt.Sprintf(":%d", p)
	}

	return carettaConfig{
		shouldResolveDns:       envOr("RESOLVE_DNS", defaultShouldResolveDns, strconv.ParseBool, anyValue[bool]),
		prometheusPort:         port,
		prometheusEndpoint:     envOr("PROMETHEUS_ENDPOINT", defaultPrometheusEndpoint, asString, anyValue[string]),
		pollingIntervalSeconds: envOr("POLL_INTERVAL", defaultPollingIntervalSeconds, strconv.Atoi, func(v int) bool { return v > 0 }),
		traverseUpHierarchy:    envOr("TRAVERSE_UP_HIERARCHY", defaultTraverseUpHierarchy, strconv.ParseBool, anyValue[bool]),
		linkTTL:                envOr("LINK_TTL", defaultLinkTTL, time.ParseDuration, func(d time.Duration) bool { return d >= 0 }),
		maxConnections:         envOr("MAX_CONNECTIONS", uint32(defaultMaxConnections), parseUint32, func(v uint32) bool { return v > 0 }),
	}
}

// envOr returns the parsed value of the variable, or def if it is unset, does not parse, or is not valid.
func envOr[T any](name string, def T, parse func(string) (T, error), valid func(T) bool) T {
	val := os.Getenv(name)
	if val == "" {
		return def
	}
	v, err := parse(val)
	if err != nil || !valid(v) {
		return def
	}
	return v
}

func asString(s string) (string, error) { return s, nil }

func parseUint32(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	return uint32(v), err
}

func anyValue[T any](T) bool { return true }
