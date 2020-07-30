package indexmanager

import (
	"context"
	"sync"
	"time"

	"github.com/inconshreveable/log15"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/workerutil"
)

// TODO - document
type Manager interface {
	// TODO - document
	Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error)
	// TODO - document
	Complete(ctx context.Context, request CompleteRequest) (bool, error)
	// TODO - document
	Heartbeat(request HeartbeatRequest)
}

// TODO - document
type ThreadedManager interface {
	Manager

	// TODO - document
	Start()
	// TODO - document
	Stop()
}

type manager struct {
	store    workerutil.Store
	requests chan interface{}
	indexers map[string]*indexerMeta
	m        sync.Mutex      // guards indexers
	ctx      context.Context // root context passed to the database
	cancel   func()          // cancels the root context
	finished chan struct{}   // signals that Start has finished
}

var _ Manager = &manager{}

// indexerMeta tracks the last request time of an index agent along with the set of
// index records which it is currently processing.
type indexerMeta struct {
	lastUpdate time.Time
	metas      []indexMeta
}

// New creates a new manager with the given store.
func New(s workerutil.Store) ThreadedManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &manager{
		store:    s,
		requests: make(chan interface{}),
		indexers: map[string]*indexerMeta{},
		ctx:      ctx,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
}

// Start runs routine that handles dequeue, complete, and heartbeat requests invoked from
// other goroutines. We handle requests that modify database transactions from a single
// goroutine to simplify bookkeeping of live transactions. This method blocks until Stop
// has been called.
func (m *manager) Start() {
	defer close(m.requests)
	defer close(m.finished)

loop:
	for {
		select {
		case req := <-m.requests:
			m.handle(req)

		case <-time.After(time.Second): // TODO - configure
			m.cleanup()

		case <-m.ctx.Done():
			break loop
		}
	}

	// TODO - close all transactions
}

// Stop will cause Start to exit after the current request. This is done by canceling the context
// passed to the database (which may cause the currently processing unit of work to fail). This
// method blocks until Start has returned. All active transactions known by the manager will be
// rolled back.
func (m *manager) Stop() {
	m.cancel()
	<-m.finished
}

// TODO - document
func (m *manager) Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error) {
	envelope := newDequeueRequestEnvelope(ctx, request)

	select {
	// Send request to processing thread
	case m.requests <- envelope:
	case <-ctx.Done():
		return store.Index{}, false, ctx.Err()
	}

	select {
	// Wait for associated response
	case response := <-envelope.response:
		if response.err != nil || !response.dequeued {
			return store.Index{}, false, response.err
		}

		m.updateIndexer(request.IndexerName, response.meta)
		return response.meta.index, true, nil

	case <-ctx.Done():
		// Handle the race condition where a transaction is successfully
		// started but not inserted into the indexer's map. We immediately
		// rollback the transaction if there is one.
		go func() {
			if response := <-envelope.response; response.dequeued {
				_ = response.meta.tx.Done(ctx.Err())
			}
		}()

		return store.Index{}, false, ctx.Err()
	}
}

// TODO - document
func (m *manager) Complete(ctx context.Context, request CompleteRequest) (bool, error) {
	envelope := newCompleteRequestEnvelope(ctx, request)

	select {
	// Send request to processing thread
	case m.requests <- envelope:
	case <-ctx.Done():
		return false, ctx.Err()
	}

	select {
	// Wait for associated response
	case response := <-envelope.response:
		return response.found, response.err

	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// TODO - document
func (m *manager) Heartbeat(request HeartbeatRequest) {
	// TODO - heartbeat also needs to send all of the outstanding
	// requests so in case one gets lost we can requeue it properly.
	m.updateIndexer(request.IndexerName)
}

// updateIndexer updates the view of an index agent by bumping its last update
// time and assigning it any supplied index meta values.
func (m *manager) updateIndexer(indexerName string, metas ...indexMeta) {
	m.m.Lock()
	defer m.m.Unlock()

	meta, ok := m.indexers[indexerName]
	if !ok {
		meta = &indexerMeta{}
	}

	meta.lastUpdate = time.Now()
	meta.metas = append(meta.metas, metas...)
}

// The following methods are run in the goroutine handled by Start and are thus
// single-threaded.

// TODO
func (m *manager) handle(rawRequest interface{}) {
	switch request := rawRequest.(type) {
	case dequeueRequestEnvelope:
		request.response <- m.dequeue(request.DequeueRequest)

	case completeRequestEnvelope:
		request.response <- m.complete(request.CompleteRequest)
	}

default:
	// TODO - error
}

// dequeue selects a queued, unlocked index record and returns an index meta value
// wrapping the record and the transaction that locks it.
func (m *manager) dequeue(request DequeueRequest) dequeueResponse {
	ctx := context.Background() // TODO - combine self, request.Context

	// TODO - make configurable
	// TODO - why is it that we block on a query once we
	// have so many running transactions?
	if m.countTotalIndexes() > 10 {
		return dequeueResponse{}
	}

	record, tx, dequeued, err := m.store.Dequeue(ctx, nil)
	if err != nil {
		return dequeueResponse{err: err}
	}
	if !dequeued {
		return dequeueResponse{}
	}

	meta := indexMeta{index: record.(store.Index), tx: tx}
	m.updateIndexer(request.IndexerName, meta)
	return dequeueResponse{meta: meta, dequeued: true}
}

// countTotalIndexes returns the number of locked index records known by this manager.
func (m *manager) countTotalIndexes() int {
	m.m.Lock()
	defer m.m.Unlock()

	count := 0
	for _, v := range m.indexers {
		count += len(v.metas)
	}

	return count
}

// complete finds the target index meta value, removes it from the index agent, marks the
// index record as complete or errored depending on the existence of an error message,
// then finalizes the transaction that locks that record.
func (m *manager) complete(request CompleteRequest) completeResponse {
	index, ok := m.findMeta(request.IndexerName, request.IndexID)
	if !ok {
		return completeResponse{}
	}

	if err := m.completeIndex(index, request.ErrorMessage); err != nil {
		return completeResponse{err: err}
	}

	return completeResponse{found: true}
}

// findMeta finds and returns an index meta value matching the given index identifier. If found,
// the meta value is removed from the index agent.
func (m *manager) findMeta(indexerName string, indexID int) (indexMeta, bool) {
	m.m.Lock()
	defer m.m.Unlock()

	metas := m.indexers[indexerName].metas

	for i, meta := range metas {
		if meta.index.ID != indexID {
			continue
		}

		metas[i] = metas[len(metas)-1]
		m.indexers[indexerName].metas = metas[:len(metas)-1]
		return meta, true
	}

	return indexMeta{}, false
}

// completeIndex marks the target index record as complete or errored depending on the
// existence of an error message, then finalizes the transaction that locks that record.
func (m *manager) completeIndex(meta indexMeta, errorMessage string) (err error) {
	ctx := context.Background() // TODO - combine self, request.Context

	if errorMessage == "" {
		_, err = meta.tx.MarkComplete(ctx, meta.index.ID)
	} else {
		_, err = meta.tx.MarkErrored(ctx, meta.index.ID, errorMessage)
	}

	return meta.tx.Done(err)
}

// cleanup rolls back the transactions assigned to every index agent which has not been
// updated longer than the death threshold.
func (m *manager) cleanup() {
	ctx := context.Background()

	for _, meta := range m.pruneIndexers() {
		delay := time.Second * 5 // TODO - configure
		if err := meta.tx.Requeue(ctx, meta.index.ID, time.Now().Add(delay)); err != nil {
			log15.Error("failed to requeue index", "error", err)
		}

		if err := meta.tx.Done(nil); err != nil {
			log15.Error("failed to close transaction", "error", err)
		}
	}
}

// TODO - document
var DeathThreshold = time.Second * 5

// pruneIndexers removes the data associated with index agents which have not been updated
// longer than the death threshold and returns all index meta values assigned to removed
// index agents.
func (m *manager) pruneIndexers() (metas []indexMeta) {
	m.m.Lock()
	defer m.m.Unlock()

	for name, meta := range m.indexers {
		if time.Since(meta.lastUpdate) <= DeathThreshold {
			continue
		}

		metas = append(metas, meta.metas...)
		delete(m.indexers, name)
	}

	return metas
}
