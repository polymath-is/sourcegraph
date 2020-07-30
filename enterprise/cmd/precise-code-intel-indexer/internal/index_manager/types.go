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

// dequeueRequestEnvelope extends a dequeue request with a request context
// and a channel on which the response is placed by the manager.
type dequeueRequestEnvelope struct {
	DequeueRequest
	context  context.Context
	response chan dequeueResponse
}

// newDequeueRequestEnvelope wraps the given dequeue request in an envelope.
func newDequeueRequestEnvelope(ctx context.Context, request DequeueRequest) dequeueRequestEnvelope {
	return dequeueRequestEnvelope{context: ctx, DequeueRequest: request, response: make(chan dequeueResponse, 1)}
}

// dequeueResponse is the response of an internal dequeue request.
type dequeueResponse struct {
	meta     indexMeta
	dequeued bool
	err      error
}

// indexMeta wraps an index record and the tranaction that is currently locking
// it for processing.
type indexMeta struct {
	index store.Index
	tx    workerutil.Store
}

// completeRequestEnvelope extends a complete request with a request context
// and a channel on which the response is placed by the manager.
type completeRequestEnvelope struct {
	CompleteRequest
	context  context.Context
	response chan completeResponse
}

// newCompleteRequestEnvelope wraps the given complete request in an envelope.
func newCompleteRequestEnvelope(ctx context.Context, request CompleteRequest) completeRequestEnvelope {
	return completeRequestEnvelope{context: ctx, CompleteRequest: request, response: make(chan completeResponse, 1)}
}

// completeResponse is the response of an internal complete request.
type completeResponse struct {
	found bool
	err   error
}
