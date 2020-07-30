package indexmanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/efritz/glock"
	"github.com/hashicorp/go-multierror"
	"github.com/inconshreveable/log15"
	"github.com/pkg/errors"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/workerutil"
	"github.com/teivah/onecontext"
)

// Manager tracks which index records are assigned to which index agents.
type Manager interface {
	// Dequeue returns a queued, unlocked index record. This record will be locked in a transaction
	// until Complete is invoked with the same indexer name and index identifier or the index agent
	// becomes unresponsive.
	Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error)

	// Complete marks the target index record as complete or errored depending on the existence of an
	// error message, then finalizes the transaction that locks that record.
	Complete(ctx context.Context, request CompleteRequest) (bool, error)

	// Heartbeat bumps the last updated time of the index agent and closes any transactions locking
	// records whose identifiers were not supplied in the request.
	Heartbeat(ctx context.Context, request HeartbeatRequest) error
}

// ThreadedManager is a manager that handles requests that modify database transactions from a single
// goroutine to simplify bookkeeping of live transactions.
type ThreadedManager interface {
	Manager

	// Start runs routine that handles dequeue, complete, and heartbeat requests invoked from other
	// goroutines. This method blocks until Stop has been called.
	Start()

	// Stop will cause Start to exit after the current request. This is done by canceling the context
	// passed to the database (which may cause the currently processing unit of work to fail). This
	// method blocks until Start has returned. All active transactions known by the manager will be
	// rolled back.
	Stop()
}

type ManagerOptions struct {
	// MaxTransactions is the maximum number of active records that can be given out to index agents.
	// The manager dequeue method will stop returning records while the number of outstanding transactions
	// is at this threshold.
	MaxTransactions int

	// RequeueDelay controls how far into the future to make an index agent's records visible to another
	// agent once it becomes unresponsive.
	RequeueDelay time.Duration

	// DeathThreshold is the minimum time since the last index agent heartbeat before the agent can be
	// considered as unresponsive. This should be configured to be longer than the index agent's heartbeat
	// interval.
	DeathThreshold time.Duration

	// CleanupInterval is the duration between cleanup invocations, in which the index records assigned to
	// dead index agents are requeued.
	CleanupInterval time.Duration

	// UnreportedMaxAge is the maximum time between an index record being dequeued and it appearing in the
	// index agent's heartbeat requests before it being considered lost.
	UnreportedIndexMaxAge time.Duration
}

type manager struct {
	store    workerutil.Store
	options  ManagerOptions
	clock    glock.Clock
	requests chan interface{}
	indexers map[string]*indexerMeta
	m        sync.Mutex      // protects indexers
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

// New creates a new manager with the given store and options.
func New(store workerutil.Store, options ManagerOptions) ThreadedManager {
	return new(store, options, glock.NewRealClock())
}

func New(store workerutil.Store, options ManagerOptions, clock glock.Clock) ThreadedManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &manager{
		store:    store,
		options:  options,
		clock:    clock,
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
// has been called. On exit, all open transactions will be rolled back.
func (m *manager) Start() {
	defer close(m.requests)
	defer close(m.finished)

loop:
	for {
		select {
		case req := <-m.requests:
			m.handle(req)

		case <-m.clock.After(m.options.CleanupInterval):
			m.cleanup()

		case <-m.ctx.Done():
			break loop
		}
	}

	m.m.Lock()
	defer m.m.Unlock()

	for _, indexer := range m.indexers {
		for _, meta := range indexer.metas {
			if err := meta.tx.Done(m.ctx.Err()); err != m.ctx.Err() {
				log15.Error(fmt.Sprintf("failed to close transaction holding index %d", meta.index.ID), "err", err)
			}
		}
	}
}

// Stop will cause Start to exit after the current request. This is done by canceling the context
// passed to the database (which may cause the currently processing unit of work to fail). This
// method blocks until Start has returned. All active transactions known by the manager will be
// rolled back.
func (m *manager) Stop() {
	m.cancel()
	<-m.finished
}

// Dequeue returns a queued, unlocked index record. This record will be locked in a transaction
// until Complete is invoked with the same indexer name and index identifier or the index agent
// becomes unresponsive.
func (m *manager) Dequeue(ctx context.Context, request DequeueRequest) (store.Index, bool, error) {
	envelope := newDequeueRequestEnvelope(ctx, request)

	select {
	case m.requests <- envelope:
	case <-ctx.Done():
		return store.Index{}, false, ctx.Err()
	}

	select {
	case response := <-envelope.response:
		if response.err != nil || !response.dequeued {
			return store.Index{}, false, response.err
		}

		return response.meta.index, true, nil

	case <-ctx.Done():
		return store.Index{}, false, ctx.Err()
	}
}

// Complete marks the target index record as complete or errored depending on the existence of an
// error message, then finalizes the transaction that locks that record.
func (m *manager) Complete(ctx context.Context, request CompleteRequest) (bool, error) {
	envelope := newCompleteRequestEnvelope(ctx, request)

	select {
	case m.requests <- envelope:
	case <-ctx.Done():
		return false, ctx.Err()
	}

	select {
	case response := <-envelope.response:
		return response.found, response.err

	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Heartbeat bumps the last updated time of the index agent and closes any transactions locking
// records whose identifiers were not supplied in the request.
func (m *manager) Heartbeat(ctx context.Context, request HeartbeatRequest) error {
	indexer, ok := m.indexers[request.IndexerName]
	if !ok {
		indexer = &indexerMeta{}
	}

	return m.requeueIndexes(ctx, m.pruneIndexes(indexer, request.IndexIDs))
}

// pruneIndexes removes the indexes whose identifier is not in the given list from the given
// index agent. This method returns the index meta values which were removed. Index meta values
// which were created very recently will be counted as live to account for the time between
// when the record is dequeued in this service and when it is added to the heartbeat requests
// from the index agent.
func (m *manager) pruneIndexes(indexer *indexerMeta, ids []int) (dead []indexMeta) {
	idMap := map[int]struct{}{}
	for _, id := range ids {
		idMap[id] = struct{}{}
	}

	m.m.Lock()
	defer m.m.Unlock()

	var live []indexMeta
	for _, meta := range indexer.metas {
		if _, ok := idMap[meta.index.ID]; ok || m.clock.Now().Sub(meta.started) < m.options.UnreportedIndexMaxAge {
			live = append(live, meta)
		} else {
			dead = append(dead, meta)
		}
	}

	indexer.metas = live
	indexer.lastUpdate = m.clock.Now()
	return dead
}

// requeueIndexes requeues the given index records.
func (m *manager) requeueIndexes(ctx context.Context, metas []indexMeta) (errs error) {
	for _, meta := range metas {
		if err := meta.tx.Requeue(ctx, meta.index.ID, m.clock.Now().Add(m.options.RequeueDelay)); err != nil {
			errs = multierror.Append(errs, errors.Wrap(err, fmt.Sprintf("failed to requeue index %d", meta.index.ID)))
		}

		if err := meta.tx.Done(nil); err != nil {
			errs = multierror.Append(errs, errors.Wrap(err, fmt.Sprintf("failed to close transaction holding index %d", meta.index.ID)))
		}
	}

	return errs
}

// The following methods are run in the goroutine handled by Start and are thus
// single-threaded. The only locks that need to be applied are to the indexers
// map, which is updated in other goroutines handling heartbeats and dequeue
// responses.

// handle dispatches requests send to the goroutine handled by Start.
func (m *manager) handle(rawRequest interface{}) {
	switch request := rawRequest.(type) {
	case dequeueRequestEnvelope:
		ctx, cancel := onecontext.Merge(m.ctx, request.context)
		response := m.dequeue(ctx, request.DequeueRequest)
		cancel()
		request.response <- response

	case completeRequestEnvelope:
		ctx, cancel := onecontext.Merge(m.ctx, request.context)
		response := m.complete(ctx, request.CompleteRequest)
		cancel()
		request.response <- response

	default:
		log15.Error(fmt.Sprintf("unexpected request value: %#v", rawRequest))
	}
}

// dequeue selects a queued, unlocked index record and returns an index meta value
// wrapping the record and the transaction that locks it.
func (m *manager) dequeue(ctx context.Context, request DequeueRequest) dequeueResponse {
	if m.countTotalIndexes() > m.options.MaxTransactions {
		return dequeueResponse{}
	}

	record, tx, dequeued, err := m.store.Dequeue(ctx, nil)
	if err != nil {
		return dequeueResponse{err: err}
	}
	if !dequeued {
		return dequeueResponse{}
	}

	m.m.Lock()
	defer m.m.Unlock()

	indexer, ok := m.indexers[request.IndexerName]
	if !ok {
		indexer = &indexerMeta{}
	}

	now := m.clock.Now()
	meta := indexMeta{index: record.(store.Index), tx: tx, started: now}
	indexer.metas = append(indexer.metas, meta)
	indexer.lastUpdate = now
	return dequeueResponse{meta: meta, dequeued: true}
}

// countTotalIndexes returns the number of locked index records known by this manager.
func (m *manager) countTotalIndexes() int {
	m.m.Lock()
	defer m.m.Unlock()

	count := 0
	for _, indexer := range m.indexers {
		count += len(indexer.metas)
	}

	return count
}

// complete finds the target index meta value, removes it from the index agent, marks the
// index record as complete or errored depending on the existence of an error message,
// then finalizes the transaction that locks that record.
func (m *manager) complete(ctx context.Context, request CompleteRequest) completeResponse {
	index, ok := m.findMeta(request.IndexerName, request.IndexID)
	if !ok {
		return completeResponse{}
	}

	if err := m.completeIndex(ctx, index, request.ErrorMessage); err != nil {
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

// completeIndex marks the target index record as complete or errored depending on the existence
// of an error message, then finalizes the transaction that locks that record.
func (m *manager) completeIndex(ctx context.Context, meta indexMeta, errorMessage string) (err error) {
	if errorMessage == "" {
		_, err = meta.tx.MarkComplete(ctx, meta.index.ID)
	} else {
		_, err = meta.tx.MarkErrored(ctx, meta.index.ID, errorMessage)
	}

	return meta.tx.Done(err)
}

// cleanup requeues every locked index record assigned to agents which have not been updated for longer
// than the death threshold.
func (m *manager) cleanup() {
	if err := m.requeueIndexes(m.ctx, m.pruneIndexers()); err != nil {
		log15.Error("failed to requeue indexes", "err", err)
	}
}

// pruneIndexers removes the data associated with index agents which have not been updated for longer
// than the death threshold and returns all index meta values assigned to removed index agents.
func (m *manager) pruneIndexers() (metas []indexMeta) {
	m.m.Lock()
	defer m.m.Unlock()

	for name, indexer := range m.indexers {
		if m.clock.Now().Sub(indexer.lastUpdate) <= m.options.DeathThreshold {
			continue
		}

		metas = append(metas, indexer.metas...)
		delete(m.indexers, name)
	}

	return metas
}
