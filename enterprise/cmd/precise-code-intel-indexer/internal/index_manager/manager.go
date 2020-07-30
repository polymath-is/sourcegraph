package indexmanager

import (
	"context"
	"sync"
	"time"

	"github.com/inconshreveable/log15"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/workerutil"
)

// TODO - move to new package

// TODO - document
type Manager struct {
	store    workerutil.Store
	m        sync.Mutex
	indexers map[string]*IndexerMeta
	requests chan interface{}
}

// TODO - document
type IndexerMeta struct {
	LastHeartbeat time.Time
	Indexes       []IndexMeta
}

// TODO - document
func NewManager(s workerutil.Store) *Manager {
	return &Manager{
		store:    s,
		indexers: map[string]*IndexerMeta{},
		requests: make(chan interface{}),
	}
}

// TODO - document
func (m *Manager) Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error) {
	envelope := newDequeueRequestEnvelope(ctx, request)
	m.requests <- envelope

	select {
	case response := <-envelope.Response:
		if response.Error != nil {
			return store.Index{}, false, response.Error
		}
		if response.Dequeued {
			return store.Index{}, false, nil
		}

		m.updateIndexer(request.IndexerName, response.Index)
		return response.Index.Index, true, nil

	case <-ctx.Done():
		return store.Index{}, false, ctx.Err()
	}
}

// TODO - document
func (m *Manager) Complete(ctx context.Context, request CompleteRequest) (bool, error) {
	envelope := newCompleteRequestEnvelope(ctx, request)
	m.requests <- envelope

	select {
	case response := <-envelope.Response:
		return response.Found, response.Error

	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// TODO - document
func (m *Manager) Heartbeat(request HeartbeatRequest) {
	// TODO - heartbeat also needs to send all of the outstanding
	// requests so in case one gets lost we can requeue it properly.
	m.updateIndexer(request.IndexerName)
}

// TODO - document
func (m *Manager) updateIndexer(indexerName string, indexes ...IndexMeta) {
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
func (m *Manager) Start() {
	ctx := context.Background()

	for {
		select {
		case req := <-m.requests:
			m.handle(req)

		case <-time.After(time.Second):
			for _, index := range m.pruneIndexes() {
				if err := index.Tx.Requeue(ctx, index.Index.ID, time.Now().Add(time.Second*5)); err != nil {
					log15.Error("failed to requeue index", "error", err)
				}

				if err := index.Tx.Done(nil); err != nil {
					log15.Error("failed to close transaction", "error", err)
				}
			}
		}

		// TODO - stop?
	}
}

// TODO - document
func (m *Manager) Stop() {
	// TODO - implement
}

// TODO - document
func (m *Manager) handle(rawRequest interface{}) {
	switch request := rawRequest.(type) {
	case dequeueRequestEnvelope:
		request.Response <- m.dequeue(request.DequeueRequest)

	case completeRequestEnvelope:
		request.Response <- m.complete(request.CompleteRequest)
	}
}

// TODO - document
func (m *Manager) dequeue(request DequeueRequest) DequeueResponse {
	ctx := context.Background() // TODO - combine self, request.Context

	// TODo - why is it that we block on a query once we
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
func (m *Manager) countTotalIndexes() int {
	m.m.Lock()
	defer m.m.Unlock()

	count := 0
	for _, v := range m.indexers {
		count += len(v.Indexes)
	}

	return count
}

// TODO - document
func (m *Manager) complete(request CompleteRequest) CompleteResponse {
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
func (m *Manager) removeIndex(indexerName string, indexID int) (IndexMeta, bool) {
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
func (m *Manager) completeIndex(index IndexMeta, errorMessage string) (err error) {
	ctx := context.Background() // TODO - combine self, request.Context

	if errorMessage == "" {
		_, err = index.Tx.MarkComplete(ctx, index.Index.ID)
	} else {
		_, err = index.Tx.MarkErrored(ctx, index.Index.ID, errorMessage)
	}

	return index.Tx.Done(err)
}

// TODO - document
func (m *Manager) pruneIndexes() (indexes []IndexMeta) {
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
