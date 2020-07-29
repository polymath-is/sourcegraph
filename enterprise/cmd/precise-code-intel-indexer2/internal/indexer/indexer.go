package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/efritz/glock"
	"github.com/inconshreveable/log15"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
)

type Indexer struct {
	options  IndexerOptions
	clock    glock.Clock
	ctx      context.Context // root context passed to commands
	cancel   func()          // cancels the root context
	wg       sync.WaitGroup  // tracks active background goroutines
	finished chan struct{}   // signals that Start has finished
}

type IndexerOptions struct {
	FrontendURL string
	Interval    time.Duration
	Metrics     IndexerMetrics
}

func NewIndexer(ctx context.Context, options IndexerOptions) *Indexer {
	return newIndexer(ctx, options, glock.NewRealClock())
}

func newIndexer(ctx context.Context, options IndexerOptions, clock glock.Clock) *Indexer {
	ctx, cancel := context.WithCancel(ctx)

	return &Indexer{
		options:  options,
		clock:    clock,
		ctx:      ctx,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
}

// Start begins polling for work from the API and indexing repositories.
func (i *Indexer) Start() {
	defer close(i.finished)

	// TODO - configure
	baseURL := "http://localhost:3189"

	//
	// TODO - need to add heartbeat
	//

loop:
	for {
		index, dequeued, err := dequeue(baseURL)
		if err != nil {
			log15.Error("failed to poll index job", "err", err)
		}

		delay := i.options.Interval
		if dequeued {
			delay = 0
			fmt.Printf("GOING TO PROCESS %v\n", index)
			var errorMessage string
			if index.ID%2 == 0 {
				errorMessage = "sdlfkjsdlfkjfd"
			}
			if err := complete(baseURL, index.ID, errorMessage); err != nil {
				log15.Error("OOPS", "err", err)
			}
		} else {
			fmt.Printf("NOTHING TO PROCESS\n")
		}

		select {
		case <-i.clock.After(delay):
		case <-i.ctx.Done():
			break loop
		}
	}

	i.wg.Wait()
}

// Stop will cause the indexer loop to exit after the current iteration. This is done by canceling the
// context passed to the subprocess functions (which may cause the currently processing unit of work
// to fail). This method blocks until all background goroutines have exited.
func (i *Indexer) Stop() {
	i.cancel()
	<-i.finished
}

// TODO - configure
const IndexerName = "foobar!!"

func dequeue(baseURL string) (index store.Index, _ bool, _ error) {
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/dequeue?indexerName=%s", baseURL, IndexerName), nil)
	if err != nil {
		return store.Index{}, false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return store.Index{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return store.Index{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return store.Index{}, false, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return store.Index{}, false, err
	}

	return index, true, nil
}

func complete(baseURL string, indexID int, errorMessage string) error {
	if errorMessage != "" {
		errorMessage = fmt.Sprintf("&errorMessage=%s", errorMessage)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/complete?indexerName=%s&indexId=%d%s", baseURL, IndexerName, indexID, errorMessage), nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return nil
}
