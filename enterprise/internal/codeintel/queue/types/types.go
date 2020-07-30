package types

// TODO - document
type DequeueRequest struct {
	IndexerName string `json:"indexerName"`
}

// TODO - document
type CompleteRequest struct {
	IndexerName  string `json:"indexerName"`
	IndexID      int    `json:"indexId"`
	ErrorMessage string `json:"errorMessage"`
}

// TODO - document
type HeartbeatRequest struct {
	IndexerName string `json:"indexerName"`
}
