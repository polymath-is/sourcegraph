package main

import (
	"log"
	"time"

	"github.com/sourcegraph/sourcegraph/internal/env"
)

var (
	rawFrontendURL              = env.Get("SRC_EXTERNAL_URL", "", "The external URL of the sourcegraph instance.")
	rawFrontendURLFromDocker    = env.Get("SRC_EXTERNAL_URL_FROM_DOCKER", "", "The external URL of the sourcegraph instance used form within an index container.")
	rawInternalProxyAuthToken   = env.Get("PRECISE_CODE_INTEL_INTERNAL_PROXY_AUTH_TOKEN", "", "The auth token supplied to the frontend.")
	rawIndexerPollInterval      = env.Get("PRECISE_CODE_INTEL_INDEXER_POLL_INTERVAL", "1s", "Interval between queries to the index queue.")
	rawIndexerHeartbeatInterval = env.Get("PRECISE_CODE_INTEL_INDEXER_HEARTBEAT_INTERVAL", "1s", "Interval between heartbeat requests.")
)

// mustGet returns the non-empty version of the given raw value fatally logs on failure.
func mustGet(rawValue, name string) string {
	if rawValue == "" {
		log.Fatalf("invalid value %q for %s: no value supplied", rawValue, name)
	}

	return rawValue
}

// mustParseInterval returns the interval version of the given raw value fatally logs on failure.
func mustParseInterval(rawValue, name string) time.Duration {
	d, err := time.ParseDuration(rawValue)
	if err != nil {
		log.Fatalf("invalid duration %q for %s: %s", rawValue, name, err)
	}

	return d
}
