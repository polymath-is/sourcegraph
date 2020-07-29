package indexer

import (
	"context"
	"fmt"
	"time"

	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/workerutil"
)

func NewIndexer(
	frontendURL string,
	pollInterval time.Duration,
	metrics IndexerMetrics,
) *workerutil.Worker {
	processor := &processor{
		frontendURL: frontendURL,
	}

	handler := workerutil.HandlerFunc(func(ctx context.Context, tx workerutil.Store, record workerutil.Record) error {
		return processor.Process(ctx, record.(store.Index))
	})

	workerMetrics := workerutil.WorkerMetrics{
		HandleOperation: metrics.ProcessOperation,
	}

	options := workerutil.WorkerOptions{
		Handler:     handler,
		NumHandlers: 1,
		Interval:    pollInterval,
		Metrics:     workerMetrics,
	}

	//
	// TODO - need to poll the frontend instead
	//

	fmt.Printf("Options: %v\n", options)
	return nil
}
