package server

import (
	"context"
	"sync"
	"time"

	"github.com/inconshreveable/log15"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/workerutil"
)

type TransactionManager struct {
	store    workerutil.Store
	m        sync.Mutex
	indexers map[string]IndexerMeta
	req      chan JobRequest
	cmp      chan CompleteRequest
}

type IndexerMeta struct {
	lastHeartbeat time.Time
	jobs          []JobMeta
}

type JobMeta struct {
	index store.Index
	tx    workerutil.Store
}

type JobRequest struct {
	resp chan JobResponse
}

type JobResponse struct {
	job      JobMeta
	dequeued bool
	err      error
}

type CompleteRequest struct {
	indexerName string
	indexID     int
	err         error
	resp        chan CompleteResponse
}
type CompleteResponse struct {
	found bool
	err   error
}

func newTransactionManager(s workerutil.Store) *TransactionManager {
	m := &TransactionManager{
		store:    s,
		indexers: map[string]IndexerMeta{},
		req:      make(chan JobRequest),
		cmp:      make(chan CompleteRequest),
	}

	go func() {
		for {
			//
			// TODO - do a more formal dispatch
			//

			select {
			case req := <-m.req:
				job, dequeued, err := m.dequeue(context.Background())
				req.resp <- JobResponse{job: job, dequeued: dequeued, err: err}

			case req := <-m.cmp:
				found, err := m.complete(req.indexerName, req.indexID, req.err)
				req.resp <- CompleteResponse{found, err}

			case <-time.After(time.Second):
				m.cleanup()
			}
		}
	}()

	return m
}

func (m *TransactionManager) complete(indexerName string, indexID int, indexErr error) (_ bool, err error) {
	m.m.Lock()
	defer m.m.Unlock()

	ctx := context.Background()
	jobs := m.indexers[indexerName].jobs

	for i, job := range jobs {
		if job.index.ID != indexID {
			continue
		}

		defer func() {
			err = job.tx.Done(nil)
		}()

		jobs[i] = jobs[len(jobs)-1]
		jobs = jobs[:len(jobs)-1] // TODO - testme

		m.indexers[indexerName] = IndexerMeta{
			lastHeartbeat: m.indexers[indexerName].lastHeartbeat,
			jobs:          jobs,
		}

		if indexErr != nil {
			_, err := job.tx.MarkErrored(ctx, job.index.ID, indexErr.Error())
			return true, err
		}

		_, err := job.tx.MarkComplete(ctx, job.index.ID)
		return true, err
	}

	return false, nil
}

func (m *TransactionManager) dequeue(ctx context.Context) (JobMeta, bool, error) {
	count := 0
	m.m.Lock()
	for _, v := range m.indexers {
		count += len(v.jobs)
	}
	m.m.Unlock()

	// TODo - why is it that we block on a query once we
	// have so many running transactions?

	// TODO - make configurable
	if count > 10 {
		return JobMeta{}, false, nil
	}

	// TODO - check current outstanding requests to not
	// blow out our open transactions.

	record, tx, dequeued, err := m.store.Dequeue(ctx, nil)
	if err != nil {
		return JobMeta{}, false, err
	}
	if !dequeued {
		return JobMeta{}, false, nil
	}

	return JobMeta{index: record.(store.Index), tx: tx}, true, nil
}

func (m *TransactionManager) Dequeue(ctx context.Context, indexerName string) (store.Index, bool, error) {
	ch := make(chan JobResponse, 1)
	m.req <- JobRequest{ch}
	resp := <-ch

	if resp.err == nil && resp.dequeued {
		m.updateIndexer(indexerName, resp.job)
		return resp.job.index, true, nil
	}

	return store.Index{}, false, resp.err
}

func (m *TransactionManager) Complete(indexerName string, indexID int, err error) (bool, error) {
	ch := make(chan CompleteResponse, 1)
	m.cmp <- CompleteRequest{indexerName, indexID, err, ch}
	resp := <-ch

	return resp.found, resp.err
}

func (m *TransactionManager) Heartbeat(indexerName string) {
	// TODO - heartbeat also needs to send all of the outstanding
	// requests so in case one gets lost we can requeue it properly.
	m.updateIndexer(indexerName)
}

func (m *TransactionManager) updateIndexer(indexerName string, jobs ...JobMeta) {
	m.m.Lock()
	defer m.m.Unlock()

	indexerMeta, ok := m.indexers[indexerName]
	if !ok {
		indexerMeta = IndexerMeta{}
	}

	indexerMeta.lastHeartbeat = time.Now()
	indexerMeta.jobs = append(indexerMeta.jobs, jobs...)
	m.indexers[indexerName] = indexerMeta
}

func (m *TransactionManager) cleanup() {
	ctx := context.Background()

	m.m.Lock()
	defer m.m.Unlock()

	for name, meta := range m.indexers {
		if time.Since(meta.lastHeartbeat) <= time.Second*5 {
			continue
		}

		for _, job := range meta.jobs {
			//
			// TODO - may up the reset count?
			//

			if err := job.tx.Requeue(ctx, job.index.ID, time.Now().Add(time.Second*5)); err != nil {
				log15.Error("failed to requeue job", "error", err)
			}

			if err := job.tx.Done(nil); err != nil {
				log15.Error("failed to close transaction", "error", err)
			}
		}

		delete(m.indexers, name)
	}
}
