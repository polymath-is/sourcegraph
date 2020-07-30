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
	Start()
	Stop()
	Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error)
	Complete(ctx context.Context, request CompleteRequest) (bool, error)
	Heartbeat(request HeartbeatRequest)
}

type manager struct {
	store    workerutil.Store
	m        sync.Mutex
	indexers map[string]*IndexerMeta
	requests chan interface{}
	ctx      context.Context // root context passed to the database
	cancel   func()          // cancels the root context
	finished chan struct{}   // signals that Start has finished
}

var _ Manager = &manager{}

// TODO - document
type IndexerMeta struct {
	LastHeartbeat time.Time
	Indexes       []IndexMeta
}

// TODO - document
func New(s workerutil.Store) Manager {
	ctx, cancel := context.WithCancel(context.Background())

	return &manager{
		store:    s,
		indexers: map[string]*IndexerMeta{},
		requests: make(chan interface{}),
		ctx:      ctx,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
}

// TODO - document
// Start runs the poll and heartbeat loops. This method blocks until all background
// goroutines have exited.
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
}

// TODO - document
// Stop will cause the indexer loop to exit after the current iteration. This is done by
// canceling the context passed to the subprocess functions (which may cause the currently
// processing unit of work to fail). This method blocks until all background goroutines have
// exited.
func (m *manager) Stop() {
	m.cancel()
	<-m.finished
}

// TODO - document
func (m *manager) Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error) {
	envelope := newDequeueRequestEnvelope(ctx, request)

	select {
	case m.requests <- envelope:
	case <-ctx.Done():
		return store.Index{}, false, ctx.Err()
	}

	select {
	case response := <-envelope.Response:
		if response.Error != nil || !response.Dequeued {
			return store.Index{}, false, response.Error
		}

		m.updateIndexer(request.IndexerName, response.Index)
		return response.Index.Index, true, nil

	case <-ctx.Done():
		go func() {
			// TODO - document
			if response := <-envelope.Response; response.Dequeued {
				_ = response.Index.Tx.Done(ctx.Err())
			}
		}()

		return store.Index{}, false, ctx.Err()
	}
}

// TODO - document
func (m *manager) Complete(ctx context.Context, request CompleteRequest) (bool, error) {
	envelope := newCompleteRequestEnvelope(ctx, request)

	select {
	case m.requests <- envelope:
	case <-ctx.Done():
		return false, ctx.Err()
	}

	select {
	case response := <-envelope.Response:
		return response.Found, response.Error

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

// TODO - document
func (m *manager) updateIndexer(indexerName string, indexes ...IndexMeta) {
	m.m.Lock()
	defer m.m.Unlock()

	indexerMeta, ok := m.indexers[indexerName]
	if !ok {
		indexerMeta = &IndexerMeta{}
	}

	indexerMeta.LastHeartbeat = time.Now()
	indexerMeta.Indexes = append(indexerMeta.Indexes, indexes...)
}

// TODO - document
func (m *manager) handle(rawRequest interface{}) {
	switch request := rawRequest.(type) {
	case dequeueRequestEnvelope:
		request.Response <- m.dequeue(request.DequeueRequest)

	case completeRequestEnvelope:
		request.Response <- m.complete(request.CompleteRequest)
	}
}

// TODO - document
func (m *manager) dequeue(request DequeueRequest) DequeueResponse {
	ctx := context.Background() // TODO - combine self, request.Context

	// TODO - why is it that we block on a query once we
	// have so many running transactions?

	// TODO - make configurable
	if m.countTotalIndexes() > 10 {
		return DequeueResponse{}
	}

	// TODO - check current outstanding requests to not
	// blow out our open transactions.

	record, tx, dequeued, err := m.store.Dequeue(ctx, nil)
	if err != nil {
		return DequeueResponse{Error: err}
	}
	if !dequeued {
		return DequeueResponse{}
	}

	index := IndexMeta{Index: record.(store.Index), Tx: tx}
	m.updateIndexer(request.IndexerName, index)
	return DequeueResponse{Index: index, Dequeued: true}
}

// TODO - document
func (m *manager) countTotalIndexes() int {
	m.m.Lock()
	defer m.m.Unlock()

	count := 0
	for _, v := range m.indexers {
		count += len(v.Indexes)
	}

	return count
}

// TODO - document
func (m *manager) complete(request CompleteRequest) CompleteResponse {
	index, ok := m.removeIndex(request.IndexerName, request.IndexID)
	if !ok {
		return CompleteResponse{}
	}

	if err := m.completeIndex(index, request.ErrorMessage); err != nil {
		return CompleteResponse{Error: err}
	}

	return CompleteResponse{Found: true}
}

// TODO - document
func (m *manager) removeIndex(indexerName string, indexID int) (IndexMeta, bool) {
	m.m.Lock()
	defer m.m.Unlock()

	indexes := m.indexers[indexerName].Indexes

	for i, index := range indexes {
		if index.Index.ID != indexID {
			continue
		}

		indexes[i] = indexes[len(indexes)-1]
		indexes = indexes[:len(indexes)-1]
		m.indexers[indexerName].Indexes = indexes
		return index, true
	}

	return IndexMeta{}, false
}

// TODO - document
func (m *manager) completeIndex(index IndexMeta, errorMessage string) (err error) {
	ctx := context.Background() // TODO - combine self, request.Context

	if errorMessage == "" {
		_, err = index.Tx.MarkComplete(ctx, index.Index.ID)
	} else {
		_, err = index.Tx.MarkErrored(ctx, index.Index.ID, errorMessage)
	}

	return index.Tx.Done(err)
}

// TODO - document
func (m *manager) cleanup() {
	ctx := context.Background()

	for _, index := range m.pruneIndexes() {
		if err := index.Tx.Requeue(ctx, index.Index.ID, time.Now().Add(time.Second*5)); err != nil {
			log15.Error("failed to requeue index", "error", err)
		}

		if err := index.Tx.Done(nil); err != nil {
			log15.Error("failed to close transaction", "error", err)
		}
	}
}

// TODO - document
func (m *manager) pruneIndexes() (indexes []IndexMeta) {
	m.m.Lock()
	defer m.m.Unlock()

	for name, meta := range m.indexers {
		if time.Since(meta.LastHeartbeat) <= time.Second*5 {
			continue
		}

		indexes = append(indexes, meta.Indexes...)
		delete(m.indexers, name)
	}

	return indexes
}
