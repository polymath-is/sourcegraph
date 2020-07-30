package types

// DequeueRequest is sent to the index queue API to lock and retrieve a
// queued index record for processing.
type DequeueRequest struct {
	// IndexerName is a unique name identifying the requesting index agent.
	IndexerName string `json:"indexerName"`
}

// CompleteRequest is sent to the index queue API once an index request
// has finished. This request is used both on success and failure.
type CompleteRequest struct {
	// IndexerName is a unique name identifying the requesting index agent.
	IndexerName string `json:"indexerName"`

	// IndexID is the identifier of the index record that was processed.
	IndexID int `json:"indexId"`

	// ErrorMessage a description of the job failure, if indexing did not succeed.
	ErrorMessage string `json:"errorMessage"`
}

// HeartbeatRequest is sent to the index queue API periodically to keep
// the transactions held for a particular index agent alive.
type HeartbeatRequest struct {
	// IndexerName is a unique name identifying the requesting index agent.
	IndexerName string `json:"indexerName"`
}
