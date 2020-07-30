package indexmanager

import (
	"context"

	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/queue/types"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/workerutil"
)

type DequeueRequest = types.DequeueRequest
type CompleteRequest = types.CompleteRequest
type HeartbeatRequest = types.HeartbeatRequest

// TODO - document
type IndexMeta struct {
	Index store.Index
	Tx    workerutil.Store
}

// TODO - document
type DequeueResponse struct {
	Index    IndexMeta
	Dequeued bool
	Error    error
}

// TODO - document
type CompleteResponse struct {
	Found bool
	Error error
}

// TODO - document
type dequeueRequestEnvelope struct {
	DequeueRequest
	Context  context.Context
	Response chan DequeueResponse
}

// TODO - document
func newDequeueRequestEnvelope(ctx context.Context, request DequeueRequest) dequeueRequestEnvelope {
	return dequeueRequestEnvelope{Context: ctx, DequeueRequest: request, Response: make(chan DequeueResponse, 1)}
}

// TODO - document
type completeRequestEnvelope struct {
	CompleteRequest
	Context  context.Context
	Response chan CompleteResponse
}

// TODO - document
func newCompleteRequestEnvelope(ctx context.Context, request CompleteRequest) completeRequestEnvelope {
	return completeRequestEnvelope{Context: ctx, CompleteRequest: request, Response: make(chan CompleteResponse, 1)}
}
