package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/inconshreveable/log15"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sourcegraph/sourcegraph/enterprise/cmd/precise-code-intel-indexer2/internal/indexer"
	"github.com/sourcegraph/sourcegraph/enterprise/cmd/precise-code-intel-indexer2/internal/server"
	"github.com/sourcegraph/sourcegraph/internal/debugserver"
	"github.com/sourcegraph/sourcegraph/internal/env"
	"github.com/sourcegraph/sourcegraph/internal/observation"
	"github.com/sourcegraph/sourcegraph/internal/trace"
	"github.com/sourcegraph/sourcegraph/internal/tracer"
)

func main() {
	env.Lock()
	env.HandleHelpFlag()
	tracer.Init()

	var (
		frontendURL         = mustGet(rawFrontendURL, "SRC_FRONTEND_INTERNAL")
		indexerPollInterval = mustParseInterval(rawIndexerPollInterval, "PRECISE_CODE_INTEL_INDEXER_POLL_INTERVAL")
	)

	observationContext := &observation.Context{
		Logger:     log15.Root(),
		Tracer:     &trace.Tracer{Tracer: opentracing.GlobalTracer()},
		Registerer: prometheus.DefaultRegisterer,
	}

	server := server.New()
	indexerMetrics := indexer.NewIndexerMetrics(observationContext)
	indexer := indexer.NewIndexer(context.Background(), indexer.IndexerOptions{
		FrontendURL: frontendURL,
		Interval:    indexerPollInterval,
		Metrics:     indexerMetrics,
	})

	go server.Start()
	go indexer.Start()
	go debugserver.Start()

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGHUP)
	<-signals

	go func() {
		// Insta-shutdown on a second signal
		<-signals
		os.Exit(0)
	}()

	server.Stop()
	indexer.Stop()
}
