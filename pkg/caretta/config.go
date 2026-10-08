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
)

type carettaConfig struct {
	shouldResolveDns       bool
	prometheusPort         string
	prometheusEndpoint     string
	pollingIntervalSeconds int
	traverseUpHierarchy    bool
	linkTTL                time.Duration
}

// environment variables based, encapsulated to enable future changes
func readConfig() carettaConfig {
	port := defaultPrometheusPort
	if val := os.Getenv("PROMETHEUS_PORT"); val != "" {
		valInt, err := strconv.Atoi(val)
		if err == nil {
			port = fmt.Sprintf(":%d", valInt)
		}
	}

	endpoint := defaultPrometheusEndpoint
	if val := os.Getenv("PROMETHEUS_ENDPOINT"); val != "" {
		endpoint = val
	}

	interval := defaultPollingIntervalSeconds
	if val := os.Getenv("POLL_INTERVAL"); val != "" {
		valInt, err := strconv.Atoi(val)
		if err == nil && valInt > 0 {
			interval = valInt
		}
	}

	shouldResolveDns := defaultShouldResolveDns
	if val := os.Getenv("RESOLVE_DNS"); val != "" {
		valBool, err := strconv.ParseBool(val)
		if err == nil {
			shouldResolveDns = valBool
		}
	}

	traverseUpHierarchy := defaultTraverseUpHierarchy
	if val := os.Getenv("TRAVERSE_UP_HIERARCHY"); val != "" {
		valBool, err := strconv.ParseBool(val)
		if err == nil {
			traverseUpHierarchy = valBool
		}
	}

	linkTTL := defaultLinkTTL
	if val := os.Getenv("LINK_TTL"); val != "" {
		valDuration, err := time.ParseDuration(val)
		if err == nil && valDuration >= 0 {
			linkTTL = valDuration
		}
	}

	return carettaConfig{
		shouldResolveDns:       shouldResolveDns,
		prometheusPort:         port,
		prometheusEndpoint:     endpoint,
		pollingIntervalSeconds: interval,
		traverseUpHierarchy:    traverseUpHierarchy,
		linkTTL:                linkTTL,
	}
}
